package model_test

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/nexusthrive/nexusthrive/kernel/model"
)

// idempotencyActor is the actor of the hash tests.
var idempotencyActor = model.Actor{Kind: model.ActorPerson, ParticipantID: "01900000-0000-7000-8000-000000000001"}

// TestIdempotencyLimits pins the limits of ADR-0026.
func TestIdempotencyLimits(t *testing.T) {
	t.Parallel()
	if model.IdempotencyTTL != 24*time.Hour {
		t.Errorf("IdempotencyTTL = %v, want 24h", model.IdempotencyTTL)
	}
	if model.IdempotencyClockTolerance != 5*time.Minute {
		t.Errorf("IdempotencyClockTolerance = %v, want 5m", model.IdempotencyClockTolerance)
	}
	if model.MaxIdempotencyKey != 128 || model.MaxRequestBody != 1<<20 || model.MaxResponseBody != 64<<10 {
		t.Errorf("limits = %d, %d, %d", model.MaxIdempotencyKey, model.MaxRequestBody, model.MaxResponseBody)
	}
}

// TestValidateIdempotencyKey guards criterion 9: a key of 1 to 128 characters
// among letters, digits and . _ : - and nothing else.
func TestValidateIdempotencyKey(t *testing.T) {
	t.Parallel()
	for name, key := range map[string]string{
		"one character": "a", "the longest": strings.Repeat("k", 128), "every kind": "A.b_c:d-1", "a UUID": "01900000-0000-7000-8000-000000000001",
	} {
		if err := model.ValidateIdempotencyKey(key); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, key := range map[string]string{
		"empty": "", "blank": "   ", "129 characters": strings.Repeat("k", 129), "with a space": "a b",
		"trailing newline": "key\n", "inner newline": "a\nb", "not ASCII": "clavé", "with a slash": "a/b",
		"with a quote": "a'b", "with NUL": "a\x00b", "leading space": " key", "tab": "a\tb",
	} {
		requireCode(t, model.ValidateIdempotencyKey(key), model.CodeInvalidInput)
	}
}

// TestCanonicalJSONIgnoresKeyOrderAndSpaces guards that the same body written
// in another order or with other blanks has one canonical form.
func TestCanonicalJSONIgnoresKeyOrderAndSpaces(t *testing.T) {
	t.Parallel()
	want := `{"a":1,"b":[true,null,"x",{"c":2,"d":3}],"z":"é"}`
	for name, body := range map[string]string{
		"canonical":     want,
		"reordered":     `{"z":"é","b":[true,null,"x",{"d":3,"c":2}],"a":1}`,
		"with blanks":   " {\n \"b\" : [ true , null , \"x\" , { \"c\" : 2 , \"d\" : 3 } ] ,\t\"a\":1, \"z\" : \"é\" }\n",
		"escaped value": `{"a":1,"b":[true,null,"x",{"c":2,"d":3}],"z":"\u00e9"}`,
	} {
		got, err := model.CanonicalJSON([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s: canonical = %s, want %s", name, got, want)
		}
	}

	// Arrays keep their order: it is part of the meaning.
	a, _ := model.CanonicalJSON([]byte(`[1,2]`))
	b, _ := model.CanonicalJSON([]byte(`[2,1]`))
	if string(a) == string(b) {
		t.Error("two arrays in another order share a canonical form")
	}
	// Numbers keep their literal text: 1 and 1.0 are different bodies, and a
	// large number is never rounded through a float.
	for _, pair := range [][2]string{{`1`, `1.0`}, {`10`, `1e1`}, {`12345678901234567890`, `12345678901234567891`}} {
		x, _ := model.CanonicalJSON([]byte(pair[0]))
		y, _ := model.CanonicalJSON([]byte(pair[1]))
		if string(x) == string(y) {
			t.Errorf("%s and %s share a canonical form", pair[0], pair[1])
		}
	}
	if got, err := model.CanonicalJSON([]byte(`{"n":12345678901234567890.123456789}`)); err != nil || string(got) != `{"n":12345678901234567890.123456789}` {
		t.Errorf("a large decimal = %s (%v), want its literal text", got, err)
	}
	// Strings that differ stay different, keys sort bytewise.
	if got, _ := model.CanonicalJSON([]byte(`{"b":1,"B":2,"a":3,"é":4}`)); string(got) != `{"B":2,"a":3,"b":1,"é":4}` {
		t.Errorf("key order = %s", got)
	}
}

// TestCanonicalJSONRejectsDuplicateKeysBadUTF8DepthAndSize guards criterion 9
// for the body: every body that is ambiguous, malformed or too big is
// invalid_input.
func TestCanonicalJSONRejectsDuplicateKeysBadUTF8DepthAndSize(t *testing.T) {
	t.Parallel()
	deep := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	deepObject := func(n int) string { return strings.Repeat(`{"a":`, n) + "1" + strings.Repeat("}", n) }
	quoted := func(n int) string { return `"` + strings.Repeat("a", n-2) + `"` }

	for name, body := range map[string]string{
		"duplicate key":              `{"a":1,"a":2}`,
		"duplicate nested key":       `{"x":{"a":1,"b":2,"a":3}}`,
		"duplicate by escape":        `{"a":1,"\u0061":2}`,
		"duplicate inside an array":  `[{"a":1,"a":1}]`,
		"invalid UTF-8 in a string":  "{\"a\":\"\xff\"}",
		"invalid UTF-8 in a key":     "{\"\xc3\x28\":1}",
		"invalid UTF-8 outside":      "\xff{}",
		"truncated UTF-8":            "{\"a\":\"\xe2\x82\"}",
		"depth of 65 arrays":         deep(65),
		"depth of 65 objects":        deepObject(65),
		"one byte over 1 MiB":        quoted(1<<20 + 1),
		"empty":                      "",
		"only blanks":                " \n\t ",
		"trailing value":             `{} {}`,
		"trailing garbage":           `{}x`,
		"trailing comma":             `{"a":1,}`,
		"truncated":                  `{"a":`,
		"single quotes":              `{'a':1}`,
		"unquoted key":               `{a:1}`,
		"NaN":                        `NaN`,
		"leading zero":               `01`,
		"unescaped control in text":  "\"a\nb\"",
		"two top-level values":       `1 2`,
		"valid then a stray bracket": `[1]]`,
	} {
		_, err := model.CanonicalJSON([]byte(body))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		requireCode(t, err, model.CodeInvalidInput)
	}
	for name, body := range map[string]string{
		"depth of 64 arrays":  deep(64),
		"depth of 64 objects": deepObject(64),
		"exactly 1 MiB":       quoted(1 << 20),
	} {
		if _, err := model.CanonicalJSON([]byte(body)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := model.CanonicalJSON(nil); err == nil {
		t.Error("a nil body was accepted")
	}
}

// TestRequestHashDiffersByOperationActorAndBody guards that the fingerprint
// binds the operation, the actor and the body, and nothing else.
func TestRequestHashDiffersByOperationActorAndBody(t *testing.T) {
	t.Parallel()
	hash := func(op string, actor model.Actor, body string) [32]byte {
		t.Helper()
		sum, err := model.RequestHash(op, actor, []byte(body))
		if err != nil {
			t.Fatalf("RequestHash(%q, %v, %q): %v", op, actor, body, err)
		}
		return sum
	}
	base := hash("test.pay", idempotencyActor, `{"a":1,"b":2}`)
	if base != hash("test.pay", idempotencyActor, ` { "b":2, "a":1 } `) {
		t.Error("the same body in another order has another fingerprint")
	}
	other := model.Actor{Kind: model.ActorPerson, ParticipantID: "01900000-0000-7000-8000-000000000002"}
	for name, got := range map[string][32]byte{
		"another operation":      hash("test.refund", idempotencyActor, `{"a":1,"b":2}`),
		"another participant":    hash("test.pay", other, `{"a":1,"b":2}`),
		"another kind of actor":  hash("test.pay", model.Actor{Kind: model.ActorAgent, ParticipantID: idempotencyActor.ParticipantID}, `{"a":1,"b":2}`),
		"another body":           hash("test.pay", idempotencyActor, `{"a":1,"b":3}`),
		"another number literal": hash("test.pay", idempotencyActor, `{"a":1.0,"b":2}`),
	} {
		if got == base {
			t.Errorf("%s: same fingerprint", name)
		}
	}
	// Bytes moved from one field to the next must not collide: every field has
	// a length in front of it.
	if hash("test.ab", model.Actor{Kind: model.ActorPerson, ParticipantID: "c"}, `{}`) ==
		hash("test.a", model.Actor{Kind: model.ActorPerson, ParticipantID: "bc"}, `{}`) {
		t.Error("moving bytes between the operation and the participant keeps the fingerprint")
	}
	if hash("test.a", model.Actor{Kind: model.ActorPerson, ParticipantID: "bc"}, `{}`) ==
		hash("test.a", model.Actor{Kind: model.ActorSystem, ParticipantID: "bc"}, `{}`) {
		t.Error("the kind of actor is not in the fingerprint")
	}

	for name, call := range map[string]func() error{
		"an empty operation": func() error { _, err := model.RequestHash("", idempotencyActor, []byte(`{}`)); return err },
		"an unknown actor":   func() error { _, err := model.RequestHash("test.pay", model.Actor{}, []byte(`{}`)); return err },
		"a body with duplicate keys": func() error {
			_, err := model.RequestHash("test.pay", idempotencyActor, []byte(`{"a":1,"a":1}`))
			return err
		},
		"an empty body": func() error { _, err := model.RequestHash("test.pay", idempotencyActor, nil); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: accepted", name)
		} else {
			requireCode(t, err, model.CodeInvalidInput)
		}
	}
}

// TestRequestHashFixedVector pins the format v1 of the fingerprint: any change
// to the label, the order of the fields or the length prefix changes it. The
// expected value was computed outside Go, from the definition in ADR-0026.
func TestRequestHashFixedVector(t *testing.T) {
	t.Parallel()
	sum, err := model.RequestHash("test.pay", idempotencyActor, []byte(`{ "b":[true,null,"x"], "a":1 }`))
	if err != nil {
		t.Fatalf("RequestHash: %v", err)
	}
	const want = "6325b8e24ab07c6676cebe0e98eb5d50e6a258ea8f06a1c61dd86b0375290235"
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("fingerprint = %s, want %s", got, want)
	}
}

// TestValidateIdempotencyResponse guards the shape of a stored response: a JSON
// object of at most 64 KiB.
func TestValidateIdempotencyResponse(t *testing.T) {
	t.Parallel()
	pad := func(n int) string { return `{"p":"` + strings.Repeat("a", n-8) + `"}` }
	for name, body := range map[string]string{"empty object": `{}`, "an object": `{"a":[1,2]}`, "exactly 64 KiB": pad(64 << 10)} {
		if err := model.ValidateIdempotencyResponse([]byte(body)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, body := range map[string]string{
		"an array": `[]`, "a string": `"x"`, "null": `null`, "a number": `1`, "a boolean": `true`, "empty": ``,
		"not JSON": `{`, "trailing": `{} {}`, "one byte over 64 KiB": pad(64<<10 + 1), "duplicate keys": `{"a":1,"a":2}`,
		"invalid UTF-8": "{\"a\":\"\xff\"}",
	} {
		err := model.ValidateIdempotencyResponse([]byte(body))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		requireCode(t, err, model.CodeInvalidInput)
	}
}

// TestNewIdempotencyRecordValidate guards what a record needs before it is
// written.
func TestNewIdempotencyRecordValidate(t *testing.T) {
	t.Parallel()
	valid := model.NewIdempotencyRecord{
		ID: "01900000-0000-7000-8000-0000000000f1", Key: "key-1", Operation: "test.pay", Actor: idempotencyActor,
		Response: []byte(`{"ok":true}`),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("a valid record: %v", err)
	}
	for _, change := range map[string]func(*model.NewIdempotencyRecord){
		"no id":              func(r *model.NewIdempotencyRecord) { r.ID = "" },
		"a nil id":           func(r *model.NewIdempotencyRecord) { r.ID = "00000000-0000-0000-0000-000000000000" },
		"a bad key":          func(r *model.NewIdempotencyRecord) { r.Key = "a b" },
		"no operation":       func(r *model.NewIdempotencyRecord) { r.Operation = "" },
		"a bad operation":    func(r *model.NewIdempotencyRecord) { r.Operation = "Test.Pay" },
		"no participant":     func(r *model.NewIdempotencyRecord) { r.Actor.ParticipantID = "" },
		"a bad participant":  func(r *model.NewIdempotencyRecord) { r.Actor.ParticipantID = "nope" },
		"an array response":  func(r *model.NewIdempotencyRecord) { r.Response = []byte(`[]`) },
		"a missing response": func(r *model.NewIdempotencyRecord) { r.Response = nil },
	} {
		in := valid
		change(&in)
		requireCode(t, in.Validate(), model.CodeInvalidInput)
	}
}

// TestCanonicalJSONRejectsLoneSurrogates guards that escapes that stand for a
// lone UTF-16 surrogate are invalid_input: the decoder would turn each of them
// into U+FFFD and bodies that differ only there would share one fingerprint.
func TestCanonicalJSONRejectsLoneSurrogates(t *testing.T) {
	t.Parallel()
	bs := string(rune(92))
	for name, body := range map[string]string{
		"a lone high surrogate":         `"` + bs + `ud800"`,
		"another lone high surrogate":   `"` + bs + `ud801"`,
		"a lone low surrogate":          `"` + bs + `udc00"`,
		"a high followed by a letter":   `"` + bs + `ud800a"`,
		"a high followed by a high":     `"` + bs + `ud800` + bs + `ud800"`,
		"a lone surrogate in a key":     `{"` + bs + `ud800":1}`,
		"upper case hex":                `"` + bs + `uD83D"`,
		"a surrogate after a backslash": `"` + bs + bs + bs + `ud800"`,
	} {
		_, err := model.CanonicalJSON([]byte(body))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		requireCode(t, err, model.CodeInvalidInput)
	}
	// Valid pairs, the replacement character itself and an escaped backslash
	// that only looks like an escape stay valid and stay different.
	forms := map[string]string{}
	for name, body := range map[string]string{
		"a valid pair":         `"` + bs + `ud83d` + bs + `ude00"`,
		"the same pair raw":    "\"😀\"",
		"the replacement":      `"` + bs + `ufffd"`,
		"the replacement raw":  "\"�\"",
		"an escaped backslash": `"` + bs + bs + `ud800"`,
	} {
		got, err := model.CanonicalJSON([]byte(body))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		forms[name] = string(got)
	}
	if forms["a valid pair"] != forms["the same pair raw"] || forms["the replacement"] != forms["the replacement raw"] {
		t.Errorf("equal texts have different forms: %v", forms)
	}
	if forms["the replacement"] == forms["an escaped backslash"] || forms["a valid pair"] == forms["the replacement"] {
		t.Errorf("different texts share a form: %v", forms)
	}
}
