// Copyright (c) DFINITY Foundation

package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Takes a terraform value and tries to convert it to a value that can be serialized
// as candid.
// Heuristics:
//   - If the value is an object with fields __didType & __didValue, use __didType
//     as the type for __didValue
//   - Otherwise, use the corresponding candid value (candid 'text' for Terraform strings,
//     candid 'record' for Terraform objects, etc)
func TFValToCandid(val tftypes.Value) (any, error) {

	// First, we try to read a wrapped value ( { __didType = ..., __didValue = ... })
	res, errWrapped := readWrappedValue(val)
	if errWrapped == nil {
		return res, nil
	}

	// Otherwise, try to read the value as unwrapped values (the order doesn't really matter)
	res, errText := readTextValue(val)
	if errText == nil {
		return res, nil
	}

	res, errRec := readRecordValue(val)
	if errRec == nil {
		return res, nil
	}

	return nil, fmt.Errorf("cannot encode value %v: %w; %w; %w", val.String(), errWrapped, errText, errRec)
}

// Read a wrapped value. Returns an error if not a wrapped value (i.e. if does not contain
// __didType & __didValue).
func readWrappedValue(val tftypes.Value) (any, error) {
	var m map[string]tftypes.Value

	err := val.As(&m)
	if err != nil {
		return nil, fmt.Errorf("not a wrapped value: %w (%v)", err, val)
	}

	idlType, ok := m["__didType"]

	if !ok {
		return nil, fmt.Errorf("not a wrapped value: no __didType: %v", val)
	}

	idlValue, ok := m["__didValue"]
	if !ok {
		return nil, fmt.Errorf("not a wrapped value: no __didValue: %v", val)
	}

	ty, err := readTextValue(idlType) // XXX: Hack to read the TF value as a Golang string
	if err != nil {
		return nil, fmt.Errorf("__didType not a string: %v", val.String())
	}

	// NOTE: Here we explicitly specify the primitive type (text, record, ...) as we
	// do _not_ want to read the value as a wrapper (even if it contains __didType &
	// __didValue).
	switch ty {
	case "text":
		return readTextValue(idlValue)
	case "record":
		return readRecordValue(idlValue)

	default:
		return nil, fmt.Errorf("unknown idl type %s for val %v", idlType.String(), val)
	}
}

// read 'val' as a text value.
func readTextValue(val tftypes.Value) (string, error) {
	var str string
	err := val.As(&str)
	if err == nil {
		return str, nil
	}

	return "", fmt.Errorf("not a string: %v", val)
}

// read 'val' as a record value.
func readRecordValue(val tftypes.Value) (map[string]any, error) {
	var m map[string]tftypes.Value

	err := val.As(&m)
	if err != nil {
		return nil, fmt.Errorf("not a record: %s", val.String())
	}
	ret := make(map[string]any)
	for k, v := range m {
		ret[k], err = TFValToCandid(v)
		if err != nil {
			return nil, err
		}
	}

	return ret, nil

}
