package options

type OpenStreamOptions struct {
	Meta []byte
}

func (opt *OpenStreamOptions) SetMeta(meta []byte) {
	opt.Meta = meta
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
	}
	return o
}
