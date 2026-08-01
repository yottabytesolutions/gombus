package wmbus

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCMACAES checks the RFC 4493 AES-CMAC test vectors, which cover the
// empty message, a partial final block and whole block messages.
func TestCMACAES(t *testing.T) {
	key := mustHex(t, "2b7e151628aed2a6abf7158809cf4f3c")

	tests := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "empty message",
			msg:  "",
			want: "bb1d6929e95937287fa37d129b756746",
		},
		{
			name: "one block",
			msg:  "6bc1bee22e409f96e93d7e117393172a",
			want: "070a16b46b4d4144f79bdd9dd04a287c",
		},
		{
			name: "partial final block",
			msg: "6bc1bee22e409f96e93d7e117393172a" +
				"ae2d8a571e03ac9c9eb76fac45af8e51" +
				"30c81c46a35ce411",
			want: "dfa66747de9ae63030ca32611497c827",
		},
		{
			name: "four blocks",
			msg: "6bc1bee22e409f96e93d7e117393172a" +
				"ae2d8a571e03ac9c9eb76fac45af8e51" +
				"30c81c46a35ce411e5fbc1191a0a52ef" +
				"f69f2445df4f9b17ad2b417be66c3710",
			want: "51f0bebf7e3b9d92fc49741779363cfe",
		},
	}

	for _, tt := range tests {
		t.Run(
			tt.name, func(t *testing.T) {
				mac, err := cmacAES(key, mustHex(t, tt.msg))
				require.NoError(t, err)
				require.Equal(t, tt.want, hex.EncodeToString(mac))
			},
		)
	}
}

func TestCMACRejectsShortKey(t *testing.T) {
	_, err := cmacAES([]byte{0x00}, nil)
	require.ErrorIs(t, err, ErrInvalidKey)
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}
