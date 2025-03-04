package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	imgGenerator "github.com/tonkeeper/opentonapi/pkg/image"
	"github.com/tonkeeper/tongo/boc"
	"github.com/tonkeeper/tongo/tlb"

	"github.com/go-faster/jx"
	"github.com/tonkeeper/opentonapi/pkg/oas"
	"github.com/tonkeeper/tongo"
)

func toError(code int, err error) *oas.ErrorStatusCode {
	return &oas.ErrorStatusCode{StatusCode: code, Response: oas.Error{Error: err.Error()}}
}

func anyToJSONRawMap(a any) map[string]jx.Raw { //todo: переписать этот ужас
	var m = map[string]jx.Raw{}
	if am, ok := a.(map[string]any); ok {
		for k, v := range am {
			m[k], _ = json.Marshal(v)
		}
		return m
	}
	t := reflect.ValueOf(a)
	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			var b []byte
			var err error
			if aj, ok := t.Field(i).Interface().(json.Marshaler); ok {
				b, err = aj.MarshalJSON()
			} else if t.Field(i).Kind() == reflect.Struct {
				m := anyToJSONRawMap(t.Field(i).Interface())
				m2 := make(map[string]json.RawMessage)
				for k, v := range m {
					m2[k] = json.RawMessage(v)
				}
				b, err = json.Marshal(m2)
			} else {
				b, err = json.Marshal(t.Field(i).Interface())
			}
			if err != nil {
				panic("some shit")
			}
			name := t.Type().Field(i).Name
			m[name] = b
		}
	default:
		panic(fmt.Sprintf("some shit %v", t.Kind()))
	}
	return m
}

func convertAccountAddress(id tongo.AccountID, book addressBook) oas.AccountAddress {
	address := oas.AccountAddress{Address: id.ToRaw()}
	if i, prs := book.GetAddressInfoByAddress(id); prs {
		if i.Name != "" {
			address.SetName(oas.NewOptString(i.Name))
		}
		if i.Image != "" {
			address.SetIcon(oas.NewOptString(imgGenerator.DefaultGenerator.GenerateImageUrl(i.Image, 200, 200)))
		}
		address.IsScam = i.IsScam
	}
	if wallet, err := book.IsWallet(id); err == nil {
		address.IsWallet = wallet
	}
	return address
}

func optIntToPointer(o oas.OptInt64) *int64 {
	if !o.IsSet() {
		return nil
	}
	return &o.Value
}

func convertOptAccountAddress(id *tongo.AccountID, book addressBook) oas.OptAccountAddress {
	if id != nil {
		return oas.OptAccountAddress{Value: convertAccountAddress(*id, book), Set: true}
	}
	return oas.OptAccountAddress{}
}

func rewriteIfNotEmpty(src, dest string) string {
	if dest != "" {
		return dest
	}
	return src
}

func convertTvmStackValue(v tlb.VmStackValue) (oas.TvmStackRecord, error) {
	switch v.SumType {
	case "VmStkNull":
		return oas.TvmStackRecord{Type: oas.TvmStackRecordTypeNull}, nil
	case "VmStkNan":
		return oas.TvmStackRecord{Type: oas.TvmStackRecordTypeNan}, nil
	case "VmStkTinyInt":
		str := fmt.Sprintf("0x%x", v.VmStkTinyInt)
		if v.VmStkTinyInt < 0 {
			str = "-0x" + str[3:]
		}
		return oas.TvmStackRecord{Type: oas.TvmStackRecordTypeNum, Num: oas.NewOptString(str)}, nil
	case "VmStkInt":
		b := big.Int(v.VmStkInt)
		str := fmt.Sprintf("0x%x", b.Bytes())
		if b.Sign() == -1 {
			str = "-" + str
		}
		return oas.TvmStackRecord{Type: oas.TvmStackRecordTypeNum, Num: oas.NewOptString(str)}, nil
	case "VmStkCell":
		boc, err := v.VmStkCell.Value.ToBocString()
		if err != nil {
			return oas.TvmStackRecord{}, err
		}
		return oas.TvmStackRecord{Type: oas.TvmStackRecordTypeCell, Cell: oas.NewOptString(boc)}, nil
	case "VmStkSlice":
		boc, err := v.VmStkSlice.Cell().ToBocString()
		if err != nil {
			return oas.TvmStackRecord{}, err
		}
		return oas.TvmStackRecord{Type: oas.TvmStackRecordTypeCell, Cell: oas.NewOptString(boc)}, nil
	case "VmStkTuple":
		return convertTuple(v.VmStkTuple)
	default:
		return oas.TvmStackRecord{}, fmt.Errorf("can't conver %v stack to rest json", v.SumType)
	}
}

func convertTuple(v tlb.VmStkTuple) (oas.TvmStackRecord, error) {
	var records []tlb.VmStackValue
	var err error
	r := oas.TvmStackRecord{Type: oas.TvmStackRecordTypeTuple}
	if v.Len == 2 && (v.Data.Tail.SumType == "VmStkTuple" || v.Data.Tail.SumType == "VmStkNull") {
		records, err = v.RecursiveToSlice()
	} else {
		records, err = v.Data.RecursiveToSlice(int(v.Len))
	}
	if err != nil {
		return r, err
	}
	for _, v := range records {
		ov, err := convertTvmStackValue(v)
		if err != nil {
			return r, err
		}
		r.Tuple = append(r.Tuple, ov)
	}
	return r, nil
}

func stringToTVMStackRecord(s string) (tlb.VmStackValue, error) {
	if s == "" {
		return tlb.VmStackValue{}, fmt.Errorf("zero length sting can't be converted to tvm stack")
	}
	if s == "NaN" {
		return tlb.VmStackValue{SumType: "VmStkNan"}, nil
	}
	if s == "Null" {
		return tlb.VmStackValue{SumType: "VmStkNull"}, nil
	}
	account, err := tongo.ParseAddress(s)
	if err == nil {
		return tlb.TlbStructToVmCellSlice(account.ID.ToMsgAddress())
	}
	if strings.HasPrefix(s, "0x") {
		b, err := hex.DecodeString(s[2:])
		if err != nil {
			return tlb.VmStackValue{}, err
		}
		i := big.Int{}
		i.SetBytes(b)
		return tlb.VmStackValue{SumType: "VmStkInt", VmStkInt: tlb.Int257(i)}, nil
	}
	isDigit := true
	for _, c := range s {
		if !unicode.IsDigit(c) {
			isDigit = false
			break
		}
	}
	if isDigit {
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return tlb.VmStackValue{}, err
		}
		return tlb.VmStackValue{SumType: "VmStkTinyInt", VmStkTinyInt: i}, nil
	}
	cells, err := boc.DeserializeBocHex(s)
	if err == nil && len(cells) == 1 {
		return tlb.CellToVmCellSlice(cells[0])
	}
	c, err := boc.DeserializeSinglRootBase64(s)
	if err != nil {
		return tlb.VmStackValue{}, err
	}
	return tlb.VmStackValue{SumType: "VmStkCell", VmStkCell: tlb.Ref[boc.Cell]{Value: *c}}, nil
}
