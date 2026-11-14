package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// IdempotencyTTL is how long an idempotency record stays in force. It equals
// the interval the database uses to stamp expires_at; a test compares both.
const IdempotencyTTL = 24 * time.Hour

const (
	// MaxIdempotencyKey is the longest accepted idempotency key, in characters.
	MaxIdempotencyKey = 128
	// MaxRequestBody is the largest request body accepted for a fingerprint, in bytes.
	MaxRequestBody = 1 << 20
	// MaxResponseBody is the largest response a record stores, in bytes.
	MaxResponseBody = 64 << 10
	// maxJSONDepth is the deepest nesting of arrays and objects a body may have.
	maxJSONDepth = 64
)

// requestHashLabel opens every fingerprint and names its format. A change of the
// format changes the label, so old and new fingerprints never collide.
const requestHashLabel = "nexusthrive.idempotency.v1"

// idempotencyKeyPattern is what an idempotency key may look like. It is narrow
// on purpose: the key reaches logs and the database, and a value with line
// breaks or control characters would forge log lines. Go's $ matches only at
// the end of the text, so a trailing newline does not pass.
var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// operationNamePattern is the shape of an operation name: two or more
// lowercase segments separated by dots. The database enforces the same shape.
var operationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// ValidateIdempotencyKey checks an idempotency key: 1 to 128 characters among
// letters, digits, dot, underscore, colon and hyphen. Anything else is
// invalid_input.
func ValidateIdempotencyKey(key string) error {
	if !idempotencyKeyPattern.MatchString(key) {
		return NewError(CodeInvalidInput,
			"the idempotency key must be 1 to 128 characters among letters, digits, dot, underscore, colon and hyphen", nil)
	}
	return nil
}

// invalidBody builds the invalid_input error of a body that cannot be
// fingerprinted. The reason never echoes the body, which may be sensitive.
func invalidBody(reason string) error {
	return NewError(CodeInvalidInput, "the request body is not canonical JSON: "+reason, nil)
}

// CanonicalJSON returns the canonical form of a JSON body: objects with their
// keys sorted bytewise and no blanks, numbers as their literal text (never
// through a float, so 1 and 1.0 stay different bodies), and strings in the
// minimal escaped form. Two bodies with one meaning share one form.
//
// A body that would be ambiguous or unsafe is invalid_input: invalid UTF-8,
// a repeated key in an object (the standard decoder would keep the last one and
// make the body ambiguous), nesting deeper than 64, more than 1 MiB, bytes after
// the value and an empty body.
func CanonicalJSON(body []byte) ([]byte, error) {
	if len(body) > MaxRequestBody {
		return nil, invalidBody("it is larger than 1 MiB")
	}
	if !utf8.Valid(body) {
		return nil, invalidBody("it is not valid UTF-8")
	}
	if !json.Valid(body) {
		return nil, invalidBody("it is not valid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var out bytes.Buffer
	if err := writeCanonical(decoder, &out, 0); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, invalidBody("it has bytes after the first value")
	}
	return out.Bytes(), nil
}

// writeCanonical reads one value from the decoder and writes its canonical form
// to out. depth is the number of containers already open around the value.
func writeCanonical(decoder *json.Decoder, out *bytes.Buffer, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return invalidBody("it is not valid JSON")
	}
	switch value := token.(type) {
	case json.Delim:
		if depth+1 > maxJSONDepth {
			return invalidBody("it is nested deeper than 64 levels")
		}
		if value == '[' {
			return writeCanonicalArray(decoder, out, depth+1)
		}
		return writeCanonicalObject(decoder, out, depth+1)
	case json.Number:
		out.WriteString(value.String())
	case string:
		writeCanonicalString(out, value)
	case bool:
		if value {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case nil:
		out.WriteString("null")
	default:
		return invalidBody("it holds a value of an unknown kind")
	}
	return nil
}

// writeCanonicalArray writes the elements of an array in their own order.
func writeCanonicalArray(decoder *json.Decoder, out *bytes.Buffer, depth int) error {
	out.WriteByte('[')
	for first := true; decoder.More(); first = false {
		if !first {
			out.WriteByte(',')
		}
		if err := writeCanonical(decoder, out, depth); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return invalidBody("it is not valid JSON")
	}
	out.WriteByte(']')
	return nil
}

// writeCanonicalObject writes the members of an object sorted by key. A repeated
// key is invalid_input.
func writeCanonicalObject(decoder *json.Decoder, out *bytes.Buffer, depth int) error {
	members := map[string][]byte{}
	for decoder.More() {
		token, err := decoder.Token()
		key, isString := token.(string)
		if err != nil || !isString {
			return invalidBody("it is not valid JSON")
		}
		if _, repeated := members[key]; repeated {
			return invalidBody("an object repeats a key")
		}
		var member bytes.Buffer
		if err := writeCanonical(decoder, &member, depth); err != nil {
			return err
		}
		members[key] = member.Bytes()
	}
	if _, err := decoder.Token(); err != nil {
		return invalidBody("it is not valid JSON")
	}
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		writeCanonicalString(out, key)
		out.WriteByte(':')
		out.Write(members[key])
	}
	out.WriteByte('}')
	return nil
}

// writeCanonicalString writes a string as JSON. Marshalling a string never
// fails, and the text is valid UTF-8 because the whole body was checked.
func writeCanonicalString(out *bytes.Buffer, text string) {
	encoded, err := json.Marshal(text)
	if err != nil {
		panic("marshal string: " + err.Error())
	}
	out.Write(encoded)
}

// RequestHash returns the fingerprint of a request: the SHA-256 of the label of
// the format, the operation, the kind and participant of the actor and the
// canonical body, each field preceded by its length in 8 bytes so that bytes
// moved from one field to the next cannot collide. The operation and the actor
// are part of it so that another operation or another actor reusing a key gets
// idempotency_conflict, never the response of someone else. The body is not
// kept: only the fingerprint is.
func RequestHash(operation string, actor Actor, body []byte) ([32]byte, error) {
	if operation == "" {
		return [32]byte{}, NewError(CodeInvalidInput, "a fingerprint needs an operation", nil)
	}
	if !actor.Kind.Valid() {
		return [32]byte{}, NewError(CodeInvalidInput, "a fingerprint needs an actor of a known kind", nil)
	}
	canonical, err := CanonicalJSON(body)
	if err != nil {
		return [32]byte{}, err
	}
	digest := sha256.New()
	for _, field := range [][]byte{
		[]byte(requestHashLabel), []byte(operation), []byte(actor.Kind), []byte(actor.ParticipantID), canonical,
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		digest.Write(length[:])
		digest.Write(field)
	}
	var sum [32]byte
	copy(sum[:], digest.Sum(nil))
	return sum, nil
}

// ValidateIdempotencyResponse checks a response before it is stored: a JSON
// object of at most 64 KiB, without repeated keys. Anything else is
// invalid_input.
func ValidateIdempotencyResponse(response []byte) error {
	if len(response) > MaxResponseBody {
		return NewError(CodeInvalidInput, "the response of an idempotent operation is larger than 64 KiB", nil)
	}
	canonical, err := CanonicalJSON(response)
	if err != nil {
		return NewError(CodeInvalidInput, "the response of an idempotent operation must be a JSON object", err)
	}
	if !strings.HasPrefix(string(canonical), "{") {
		return NewError(CodeInvalidInput, "the response of an idempotent operation must be a JSON object", nil)
	}
	return nil
}

// NewIdempotencyRecord is what the engine gives to store the record of one
// execution. The generation, the expiry and the space are not here: the database
// stamps them.
type NewIdempotencyRecord struct {
	// ID is a UUID v7 and is also the identifier of the execution.
	ID        string
	Key       string
	Operation string
	Actor     Actor
	// RequestHash is the fingerprint of the request (see RequestHash).
	RequestHash [32]byte
	// Response is the JSON object the first execution returned.
	Response json.RawMessage
}

// Validate checks the record before it is written.
func (r NewIdempotencyRecord) Validate() error {
	if parsed, err := uuid.Parse(r.ID); err != nil || parsed == uuid.Nil {
		return NewError(CodeInvalidInput, "the record id must be a non-nil UUID", nil)
	}
	if err := ValidateIdempotencyKey(r.Key); err != nil {
		return err
	}
	if len(r.Operation) > 128 || !operationNamePattern.MatchString(r.Operation) {
		return NewError(CodeInvalidInput, "the operation of a record must be two or more lowercase segments separated by dots", nil)
	}
	if parsed, err := uuid.Parse(r.Actor.ParticipantID); err != nil || parsed == uuid.Nil {
		return NewError(CodeInvalidInput, "the actor of a record must stand for a participant", nil)
	}
	return ValidateIdempotencyResponse(r.Response)
}

// IdempotencyRecord is a stored record, as the database holds it.
type IdempotencyRecord struct {
	ID      string
	SpaceID string
	Key     string
	// Generation is 1 for the first record of a key and grows by one each time a
	// new execution follows an expired record.
	Generation         int
	Operation          string
	ActorParticipantID string
	RequestHash        [32]byte
	// Response is the stored JSON object, in the form of the database.
	Response json.RawMessage
	// CreatedAt and ExpiresAt are in UTC.
	CreatedAt time.Time
	ExpiresAt time.Time
}
