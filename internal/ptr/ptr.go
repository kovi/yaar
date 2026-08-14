package ptr

func Val[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func Of[T any](v T) *T {
	return &v
}
