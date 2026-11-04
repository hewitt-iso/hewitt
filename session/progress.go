package session

import (
	"fmt"
	"io"
)

// reportStep announces work before a potentially slow operation. Messages must
// describe operations, never include credential values or subprocess input.
func reportStep(w io.Writer, message string) error {
	if _, err := fmt.Fprintf(w, "[hewitt] %s...\n", message); err != nil {
		return fmt.Errorf("report launch progress: %w", err)
	}
	return nil
}
