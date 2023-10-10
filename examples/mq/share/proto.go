package share

type Claim struct {
	Role  string `json:"role"` // producer or consumer
	Topic string `json:"topic"`
}
