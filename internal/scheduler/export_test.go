package scheduler

// SetPath overrides the PATH that the backend s captured at construction.
func SetPath(s Scheduler, path string) {
	switch b := s.(type) {
	case *launchd:
		b.envPath = path
	case *systemd:
		b.envPath = path
	}
}
