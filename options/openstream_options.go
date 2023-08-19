package options

type OpenStreamOptions struct {
	Meta []byte
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
