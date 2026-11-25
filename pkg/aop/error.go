package aop

type PageError struct {
	Message  string
	Code     int
	BusiCode int
}

func (p PageError) Error() string {
	return p.Message
}

func (p PageError) String() string {
	return p.Message
}
