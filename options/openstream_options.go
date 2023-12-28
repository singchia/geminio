package options

type OpenStreamOptions struct {
	Meta []byte
	Peer *string
}

func (opt *OpenStreamOptions) SetMeta(meta []byte) {
	opt.Meta = meta
}

func (opt *OpenStreamOptions) SetPeer(peer string) {
	opt.Peer = &peer
}

func OpenStream() *OpenStreamOptions {
	return &OpenStreamOptions{}
}

func MergeOpenStreamOptions(opts ...*OpenStreamOptions) *OpenStreamOptions {
	o := &OpenStreamOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if opt.Meta != nil {
			o.Meta = opt.Meta
		}
		if opt.Peer != nil {
			o.Peer = opt.Peer
		}
	}
	return o
}
