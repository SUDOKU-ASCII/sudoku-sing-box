// Code generated from the v0.5.1 release sources; DO NOT EDIT.

package protocol

import "strings"

// goldenEncode lists WriteAddress inputs with the exact bytes the reference produced.
var goldenEncode = []struct {
	addr string
	wire string
}{
	{"1.2.3.4:8080", "01010203041f90"},
	{"0.0.0.0:0", "01000000000000"},
	{"255.255.255.255:65535", "01ffffffffffff"},
	{"[2001:db8::1]:443", "0420010db800000000000000000000000101bb"},
	{"[::1]:80", "04000000000000000000000000000000010050"},
	{"[::]:1", "04000000000000000000000000000000000001"},
	{"[fe80::1%eth0]:53", "04fe8000000000000000000000000000010035"},
	{"[::ffff:1.2.3.4]:80", "01010203040050"},
	{"example.com:53", "030b6578616d706c652e636f6d0035"},
	{"localhost:1", "03096c6f63616c686f73740001"},
	{"a:65535", "030161ffff"},
	{strings.Repeat("a", 255) + ":80", "03ff" + strings.Repeat("61", 255) + "0050"},
}

// goldenEncodeReject lists inputs the reference refused.
var goldenEncodeReject = []string{
	"example.com",
	"1.2.3.4:99999",
	"1.2.3.4:x",
	strings.Repeat("a", 256) + ":80",
}

// goldenDecode lists ReadAddress inputs with what the reference returned.
var goldenDecode = []struct {
	wire string
	host string
	typ  byte
	ip   string
}{
	{"01010203041f90", "1.2.3.4:8080", 1, "1.2.3.4"},
	{"0400000000000000000000ffff010203040050", "1.2.3.4:80", 4, "1.2.3.4"},
	{"0420010db800000000000000000000000101bb", "[2001:db8::1]:443", 4, "2001:db8::1"},
	{"030b6578616d706c652e636f6d0035", "example.com:53", 3, ""},
	{"03ff" + strings.Repeat("61", 255) + "0050", strings.Repeat("a", 255) + ":80", 3, ""},
	{"0301610001", "a:1", 3, ""},
}

// goldenDecodeReject lists ReadAddress inputs the reference refused.
var goldenDecodeReject = []string{
	"",
	"02000050",
	"03000050",
	"0401020304",
	"010102",
	"030568656c6c6f",
	"01050006",
}
