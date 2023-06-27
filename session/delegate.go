package session

type Delegate interface {
	// if SessionOnline set, the session accept channel won't work
	SessionOnline(Session) error
	SessionOffline(Session) error
}
