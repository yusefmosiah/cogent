package cli

import "fmt"

type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit with code %d", e.Code)
	}

	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

func exitf(code int, format string, args ...any) error {
	return &ExitError{
		Code: code,
		Err:  fmt.Errorf(format, args...),
	}
}
