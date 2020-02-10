// Copyright 2020 ChainSafe Systems (ON) Corp.
// This file is part of gossamer.
//
// The gossamer library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The gossamer library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the gossamer library. If not, see <http://www.gnu.org/licenses/>.
package codec

import (
	"bytes"
	"errors"
	"math/big"
	"reflect"
	"testing"

	"github.com/ChainSafe/gossamer/consensus/babe/types"
	"github.com/ChainSafe/gossamer/crypto/sr25519"
	"github.com/stretchr/testify/require"
)

func TestDecodePtrFixedWidthInts(t *testing.T) {
	for _, test := range decodeFixedWidthIntTestsInt8 {
		var res int8
		err := DecodePtr(test.val, &res)
		if err != nil {
			t.Error(err)
		} else if res != test.output {
			t.Errorf("Fail: input %d got %d expected %d", test.val, res, test.output)
		}

	}

	for _, test := range decodeFixedWidthIntTestsUint8 {
		var res uint8
		err := DecodePtr(test.val, &res)
		if err != nil {
			t.Error(err)
		} else if res != test.output {
			t.Errorf("Fail: input %d got %d expected %d", test.val, res, test.output)
		}
	}

	for _, test := range decodeFixedWidthIntTestsInt16 {
		var res int16
		err := DecodePtr(test.val, &res)
		if err != nil {
			t.Error(err)
		} else if res != test.output {
			t.Errorf("Fail: input %d got %d expected %d", test.val, res, test.output)
		}
	}

	for _, test := range decodeFixedWidthIntTestsUint16 {
		var res uint16
		err := DecodePtr(test.val, &res)
		if err != nil {
			t.Error(err)
		} else if res != test.output {
			t.Errorf("Fail: input %d got %d expected %d", test.val, res, test.output)
		}
	}

	for _, test := range decodeFixedWidthIntTestsInt32 {
		var res int32
		err := DecodePtr(test.val, &res)
		if err != nil {
			t.Error(err)
		} else if res != test.output {
			t.Errorf("Fail: input %d got %d expected %d", test.val, res, test.output)
		}
	}

	for _, test := range decodeFixedWidthIntTestsUint32 {
		var res uint32
		err := DecodePtr(test.val, &res)
		if err != nil {
			t.Error(err)
		} else if res != test.output {
			t.Errorf("Fail: input %d got %d expected %d", test.val, res, test.output)
		}
	}

	for _, test := range decodeFixedWidthIntTestsInt64 {
		var res int64
		err := DecodePtr(test.val, &res)
		if err != nil {
			t.Error(err)
		} else if res != test.output {
			t.Errorf("Fail: input %d got %d expected %d", test.val, res, test.output)
		}
	}

	for _, test := range decodeFixedWidthIntTestsUint64 {
		var res uint64
		err := DecodePtr(test.val, &res)
		if err != nil {
			t.Error(err)
		} else if res != test.output {
			t.Errorf("Fail: input %d got %d expected %d", test.val, res, test.output)
		}
	}

	for _, test := range decodeFixedWidthIntTestsInt {
		var res int
		err := DecodePtr(test.val, &res)
		if err != nil {
			t.Error(err)
		} else if res != test.output {
			t.Errorf("Fail: input %d got %d expected %d", test.val, res, test.output)
		}
	}

	for _, test := range decodeFixedWidthIntTestsUint {
		var res uint
		err := DecodePtr(test.val, &res)
		if err != nil {
			t.Error(err)
		} else if res != test.output {
			t.Errorf("Fail: input %d got %d expected %d", test.val, res, test.output)
		}
	}
}

func TestDecodePtrBigInts(t *testing.T) {
	for _, test := range decodeBigIntTests {
		res := big.NewInt(0)
		err := DecodePtr(test.val, res)
		if err != nil {
			t.Error(err)
		} else if res.Cmp(test.output) != 0 {
			t.Errorf("Fail: got %s expected %s", res.String(), test.output.String())
		}
	}
}

func TestLargeDecodePtrByteArrays(t *testing.T) {
	if testing.Short() {
		t.Skip("\033[33mSkipping memory intesive test for TestDecodePtrByteArrays in short mode\033[0m")
	} else {
		for _, test := range largeDecodeByteArrayTests {
			var result = make([]byte, len(test.output))
			err := DecodePtr(test.val, result)
			if err != nil {
				t.Error(err)
			} else if !bytes.Equal(result, test.output) {
				t.Errorf("Fail: got %d expected %d", len(result), len(test.output))
			}
		}
	}
}

func TestDecodePtrByteArrays(t *testing.T) {
	for _, test := range decodeByteArrayTests {
		var result = make([]byte, len(test.output))
		err := DecodePtr(test.val, result)
		if err != nil {
			t.Error(err)
		} else if !bytes.Equal(result, test.output) {
			t.Errorf("Fail: got %d expected %d", len(result), len(test.output))
		}
	}
}

func TestDecodePtrBool(t *testing.T) {
	for _, test := range decodeBoolTests {
		var result bool
		err := DecodePtr([]byte{test.val}, &result)
		if err != nil {
			t.Error(err)
		} else if result != test.output {
			t.Errorf("Fail: got %t expected %t", result, test.output)
		}
	}

	var result bool = true
	err := DecodePtr([]byte{0xff}, &result)
	if err == nil {
		t.Error("did not error for invalid bool")
	} else if result {
		t.Errorf("Fail: got %t expected false", result)
	}
}

func TestDecodePtrTuples(t *testing.T) {
	for _, test := range decodeTupleTests {
		err := DecodePtr(test.val, test.t)
		if err != nil {
			t.Error(err)
		} else if !reflect.DeepEqual(test.t, test.output) {
			t.Errorf("Fail: got %d expected %d", test.val, test.output)
		}
	}
}

func TestDecodePtrArrays(t *testing.T) {
	for _, test := range decodeArrayTests {
		err := DecodePtr(test.val, test.t)
		if err != nil {
			t.Error(err)
		} else if !reflect.DeepEqual(test.t, test.output) {
			t.Errorf("Fail: got %d expected %d", test.t, test.output)
		}
	}
}

// test decoding with DecodeCustom on BabeHeader type
func TestDecodeCustom_DecodeBabeHeader(t *testing.T) {
	// arbitrary test data
	expected := &types.BabeHeader{
		VrfOutput:          [sr25519.VrfOutputLength]byte{0, 91, 50, 25, 214, 94, 119, 36, 71, 216, 33, 152, 85, 184, 34, 120, 61, 161, 164, 223, 76, 53, 40, 246, 76, 38, 235, 204, 43, 31, 179, 28},
		VrfProof:           [sr25519.VrfProofLength]byte{120, 23, 235, 159, 115, 122, 207, 206, 123, 232, 75, 243, 115, 255, 131, 181, 219, 241, 200, 206, 21, 22, 238, 16, 68, 49, 86, 99, 76, 139, 39, 0, 102, 106, 181, 136, 97, 141, 187, 1, 234, 183, 241, 28, 27, 229, 133, 8, 32, 246, 245, 206, 199, 142, 134, 124, 226, 217, 95, 30, 176, 246, 5, 3},
		BlockProducerIndex: 17,
		SlotNumber:         420,
	}
	encoded := []byte{0, 91, 50, 25, 214, 94, 119, 36, 71, 216, 33, 152, 85, 184, 34, 120, 61, 161, 164, 223, 76, 53, 40, 246, 76, 38, 235, 204, 43, 31, 179, 28, 120, 23, 235, 159, 115, 122, 207, 206, 123, 232, 75, 243, 115, 255, 131, 181, 219, 241, 200, 206, 21, 22, 238, 16, 68, 49, 86, 99, 76, 139, 39, 0, 102, 106, 181, 136, 97, 141, 187, 1, 234, 183, 241, 28, 27, 229, 133, 8, 32, 246, 245, 206, 199, 142, 134, 124, 226, 217, 95, 30, 176, 246, 5, 3, 17, 0, 0, 0, 0, 0, 0, 0, 164, 1, 0, 0, 0, 0, 0, 0}
	decodedBabeHeader := new(types.BabeHeader)

	err := DecodeCustom(encoded, decodedBabeHeader)
	require.Nil(t, err)
	require.Equal(t, expected, decodedBabeHeader)
}

// add Decode func to MockTypeA
func (tr *MockTypeA) Decode(in []byte) error {
	return DecodePtr(in, tr)
}

// test decoding for MockTypeA (which has Decode func)
func TestDecodeCustom_DecodeMockTypeA(t *testing.T) {
	expected := &MockTypeA{A: "hello"}
	encoded := []byte{20, 104, 101, 108, 108, 111}
	mockType := new(MockTypeA)

	err := DecodeCustom(encoded, mockType)
	require.Nil(t, err)
	require.Equal(t, expected, mockType)
}

// test decoding for MockTypeB (which does not have Decode func)
func TestDecodeCustom_DecodeMockTypeB(t *testing.T) {
	expected := &MockTypeB{A: "hello"}
	encoded := []byte{20, 104, 101, 108, 108, 111}
	mockType := new(MockTypeB)

	err := DecodeCustom(encoded, mockType)
	require.Nil(t, err)
	require.Equal(t, expected, mockType)
}

// add Decode func to MockTypeC which will return fake data (so we'll know when it was called)
func (tr *MockTypeC) Decode(in []byte) error {
	tr.A = "goodbye"
	return nil
}

// test decoding for MockTypeC (which has Decode func that returns fake data (A: "goodbye"))
func TestDecodeCustom_DecodeMockTypeC(t *testing.T) {
	expected := &MockTypeC{A: "goodbye"}
	encoded := []byte{20, 104, 101, 108, 108, 111}
	mockType := new(MockTypeC)

	err := DecodeCustom(encoded, mockType)
	require.Nil(t, err)
	require.Equal(t, expected, mockType)
}

// add Decode func to MockTypeD which will return an error
func (tr *MockTypeD) Decode(in []byte) error {
	return errors.New("error decoding")
}

// test decoding for MockTypeD (which has Decode func that returns error)
func TestDecodeCustom_DecodeMockTypeD(t *testing.T) {
	encoded := []byte{20, 104, 101, 108, 108, 111}
	mockType := new(MockTypeD)

	err := DecodeCustom(encoded, mockType)
	require.EqualError(t, err, "error decoding")
}
