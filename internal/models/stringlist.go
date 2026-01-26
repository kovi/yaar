package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

type StringList []string

// Value: Convert Go slice to JSON string for the Database
func (l StringList) Value() (driver.Value, error) {
	if l == nil {
		return "[]", nil
	}
	return json.Marshal(l)
}

// Scan: Convert Database value (string or []byte) back into Go slice
func (l *StringList) Scan(value interface{}) error {
	if value == nil {
		*l = StringList{}
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("failed to unmarshal StringList: expected string or []byte, got %T", value)
	}

	return json.Unmarshal(bytes, l)
}
