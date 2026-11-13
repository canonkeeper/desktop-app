// SPDX-License-Identifier: MIT

package provider

import (
	"errors"
	"testing"
)

func TestDecodeEventHeaderKeepsExactJSONContract(t *testing.T) {
	for _, test := range []struct {
		data     string
		valid    bool
		typeName string
	}{
		{`{"type":"response.output_text.delta","delta":"x"}`, true, "response.output_text.delta"},
		{`{"type":"error","error":{"code":"x"}}`, true, "error"},
		{`{"type":"response.created","error":null}`, true, "response.created"},
		{`{"type":7,"delta":"x"}`, true, ""},
		{`null`, true, ""},
		{`{"type":"a"} {"type":"b"}`, false, ""},
		{`{"type":"a"`, false, ""},
		{`[1]`, false, ""},
		{`"text"`, false, ""},
	} {
		header, err := decodeEventHeader([]byte(test.data))
		var failure *Error
		if test.valid != (err == nil) || err != nil && (!errors.As(err, &failure) || failure.Code != ErrorCodeInvalidStream) {
			t.Fatalf("%s: err=%v", test.data, err)
		}
		if header.Type != test.typeName {
			t.Fatalf("%s: header=%+v", test.data, header)
		}
	}
}
