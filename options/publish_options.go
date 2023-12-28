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
}

func (opt *PublishOptions) SetTimeout(timeout time.Duration) {
	opt.Timeout = &timeout
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

	}
	return po
}

type NewMessageOptions struct {
	Custom []byte
	Cnss   *Cnss
	Topic  *string
}

func (opt *NewMessageOptions) SetCustom(data []byte) {
	opt.Custom = data
}

func (opt *NewMessageOptions) SetCnss(cnss Cnss) {
	opt.Cnss = &cnss
}

func (opt *NewMessageOptions) SetTopic(topic string) {
	opt.Topic = &topic
}

func NewMessage() *NewMessageOptions {
	return &NewMessageOptions{}
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
		if opt.Cnss != nil {
			no.Cnss = opt.Cnss
		}
		if opt.Topic != nil {
			no.Topic = opt.Topic
		}
	}
	return no
}
