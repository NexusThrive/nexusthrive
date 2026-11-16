// Package gateway is the HTTP door of the kernel. It holds no state and never
// sees the database: it receives only the execution Engine (ADR-0035), and an
// Actor exists only after a token has been verified here and resolved to a
// participant of the space in the path.
package gateway
