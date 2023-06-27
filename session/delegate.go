package session

type Delegate interface {
	SessionOnline(SessionDescriber) error
	SessionOffline(SessionDescriber) error
}
