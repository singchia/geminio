package options

import "time"

type CallOptions struct {
	Timeout *time.Duration
}

func (opt *CallOptions) SetTimeout(timeout time.Duration) {
	opt.Timeout = &timeout
}

func Call() *CallOptions {
	return &CallOptions{}
}

func MergeCallOptions(opts ...*CallOptions) *CallOptions {
	co := &CallOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if opt.Timeout != nil {
			co.Timeout = opt.Timeout
		}
	}
	return co
}
