package responseparser

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const MaxDepth = 64

func Decode(data []byte) (any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, ErrEmptyBody
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}

	if decoder.More() {
		return nil, fmt.Errorf("%w: unexpected data after the JSON value", ErrInvalidJSON)
	}

	if err := checkDepth(value, 0); err != nil {
		return nil, err
	}
	return value, nil
}

func checkDepth(value any, depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("%w: deeper than %d levels", ErrTooDeep, MaxDepth)
	}
	switch v := value.(type) {
	case map[string]any:
		for _, child := range v {
			if err := checkDepth(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := checkDepth(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
