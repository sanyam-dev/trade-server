package utils

// Truncate shortens s to at most n bytes with an ellipsis when longer.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
