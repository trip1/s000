package functions

import (
	"fmt"
	"strings"
)

func NewRuntime(name string) (Runtime, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case RuntimeWasmer:
		return &wasmerRuntime{}, nil
	case RuntimeWazero:
		return &wazeroRuntime{}, nil
	default:
		return nil, fmt.Errorf("functions: unknown runtime %q", name)
	}
}
