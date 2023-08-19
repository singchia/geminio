package options

import "time"

// message related defination
type Cnss byte

const (
	CnssAtMostOnce  Cnss = 1
	CnssAtLeastOnce Cnss = 2
)

type PublishOptions struct {
	Timeout *time.Duration
	Cnss    *Cnss
}

func Publish() *PublishOptions {
	return &PublishOptions{}
}

func NewPublishOptions(opts ...*PublishOptions) *PublishOptions {
	po := &PublishOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if opt.Timeout != nil {
			po.Timeout = opt.Timeout
		}
		if opt.Cnss != nil {
			po.Cnss = opt.Cnss
		}
	}
	return po
}
