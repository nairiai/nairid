package clients

import "fmt"

// ArgsForLog is args for a log line: the prompt, a positional entry, is left out and only its
// length is shown, so no message text reaches the log.
func ArgsForLog(args []string, prompt string) string {
	shown := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == prompt {
			continue
		}
		shown = append(shown, arg)
	}
	return fmt.Sprintf("%v (prompt %d chars)", shown, len(prompt))
}
