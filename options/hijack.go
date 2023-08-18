package options

type HijackOptions struct {
	Match   *bool
	Pattern *string
}

func Hijack() *HijackOptions {
	return &HijackOptions{}
}

func MergeHijackOptions(opts ...*HijackOptions) *HijackOptions {
	ho := &HijackOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if opt.Match != nil {
			ho.Match = opt.Match
		}
		if opt.Pattern != nil {
			ho.Pattern = opt.Pattern
		}
	}
	return ho
}
