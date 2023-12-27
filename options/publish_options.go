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
	Topic   *string
}

func (opt *PublishOptions) SetTimeout(timeout time.Duration) {
	opt.Timeout = &timeout
}

func (opt *PublishOptions) SetCnss(cnss Cnss) {
	opt.Cnss = &cnss
}

func (opt *PublishOptions) SetTopic(topic string) {
	opt.Topic = &topic
}

func Publish() *PublishOptions {
	return &PublishOptions{}
}

func MergePublishOptions(opts ...*PublishOptions) *PublishOptions {
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

type NewMessageOptions struct {
	Custom []byte
}

func (opt *NewMessageOptions) SetCustom(data []byte) {
	opt.Custom = data
}

func MergeNewMessageOptions(opts ...*NewMessageOptions) *NewMessageOptions {
	no := &NewMessageOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if opt.Custom != nil {
			no.Custom = opt.Custom
		}
	}
	return no
}
