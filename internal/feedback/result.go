package feedback

func ForOutcome(title, status string) *DisplayResult {
	state := Success
	switch status {
	case "failed":
		state = Error
	case "cancelled":
		state = Warning
	case "running":
		state = Running
	case "queued":
		state = Idle
	}
	return &DisplayResult{Title: title, State: state}
}
