package server

// disconnect_reason.go maps the disconnect-reason taxonomy tokens (set at the drop
// seams via Client.SetDisconnectReason / carried as log attrs) to the one-line human
// strings the TUI-02 leave log surfaces. The token set is the full taxonomy:
//
//	login_failure / config_failure  → "login failure"
//	protocol_mismatch / protocol_error → "protocol error"
//	timeout                          → "timeout"
//	kicked / backpressure / write_error → "kick"
//	quit (the default, or "")        → "clean quit"
//
// The five TUI-02 categories are kick / timeout / protocol error / clean quit / login
// failure (matching the phase brief wording). reasonHuman is the single mapping point so
// the leave line and any future reason-display share one vocabulary.

// reasonHuman returns the human-readable detail for a disconnect-reason token. An unknown
// token passes through unchanged so a newly-added reason is never silently lost (it shows
// up verbatim in the log until it is mapped here).
func reasonHuman(token string) string {
	switch token {
	case "", "quit":
		return "clean quit"
	case "timeout":
		return "timeout"
	case "protocol_error", "protocol_mismatch":
		return "protocol error"
	case "login_failure", "config_failure":
		return "login failure"
	case "kicked", "backpressure", "write_error":
		return "kick"
	default:
		return token
	}
}
