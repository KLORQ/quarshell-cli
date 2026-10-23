package service

// canonicalMainCommand copies the user's command without shell rewriting.
func canonicalMainCommand(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	return append([]string(nil), argv...)
}
