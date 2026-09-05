// Command go-api-gen generates Go types, servers, clients, and self-contained
// OpenAPI documents from a workspace of composable specs.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra has already reported usage errors; anything else is ours to
		// print, and it is often multi-line guidance rather than one clause.
		var silent silentError
		if !errors.As(err, &silent) {
			fmt.Fprintf(os.Stderr, "go-api-gen: %v\n", err)
		}
		os.Exit(1)
	}
}

// silentError marks an error whose message has already been shown.
type silentError struct{ error }
