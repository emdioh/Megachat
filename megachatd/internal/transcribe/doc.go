// Package transcribe is the Go port of the Python transcription stack:
// transcription/{client,service,config}.py (cloud clients and the queue
// worker), server/transcribe.py (the service host), server/autotranscribe.py
// (automatic voice-note transcription and the "!" chat commands) and
// server/ops/transcribe.py (the transcribe.*, autotranscribe.* and
// config.set_openai_key operations).
//
// The package does not import internal/core: everything it needs from the
// rest of the server is a small interface in Deps.
package transcribe
