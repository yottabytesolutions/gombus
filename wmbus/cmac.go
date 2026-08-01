package wmbus

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
)

// cmacRb is the constant of RFC 4493 for a 128 bit block cipher: the
// representation of the polynomial x^128 + x^7 + x^2 + x + 1.
const cmacRb = 0x87

// cmacAES computes the AES-CMAC of msg per RFC 4493. The key derivation of
// security mode 7 needs it and the standard library has no CMAC, so it lives
// here rather than pulling in a dependency.
func cmacAES(key, msg []byte) ([]byte, error) {
	if len(key) != aes.BlockSize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	k1, k2 := cmacSubkeys(block)

	// The last block is XORed with K1 when the message is a whole number of
	// blocks, and with K2 after 0x80 padding when it is not.
	last := make([]byte, aes.BlockSize)
	complete := len(msg) > 0 && len(msg)%aes.BlockSize == 0
	var head []byte
	if complete {
		head = msg[:len(msg)-aes.BlockSize]
		subtle.XORBytes(last, msg[len(msg)-aes.BlockSize:], k1)
	} else {
		n := len(msg) - len(msg)%aes.BlockSize
		head = msg[:n]
		padded := make([]byte, aes.BlockSize)
		copy(padded, msg[n:])
		padded[len(msg)-n] = 0x80
		subtle.XORBytes(last, padded, k2)
	}

	mac := make([]byte, aes.BlockSize)
	buf := make([]byte, aes.BlockSize)
	for i := 0; i < len(head); i += aes.BlockSize {
		subtle.XORBytes(buf, mac, head[i:i+aes.BlockSize])
		block.Encrypt(mac, buf)
	}
	subtle.XORBytes(buf, mac, last)
	block.Encrypt(mac, buf)
	return mac, nil
}

// cmacSubkeys derives K1 and K2 from the cipher, per RFC 4493 section 2.3.
func cmacSubkeys(block cipher.Block) (k1, k2 []byte) {
	zero := make([]byte, aes.BlockSize)
	l := make([]byte, aes.BlockSize)
	block.Encrypt(l, zero)
	k1 = shiftLeftOne(l)
	k2 = shiftLeftOne(k1)
	return k1, k2
}

// shiftLeftOne shifts a block left by one bit and conditionally XORs in Rb,
// which is the doubling operation of the CMAC subkey derivation.
func shiftLeftOne(in []byte) []byte {
	out := make([]byte, len(in))
	overflow := in[0]&0x80 != 0
	for i := range in {
		out[i] = in[i] << 1
		if i+1 < len(in) {
			out[i] |= in[i+1] >> 7
		}
	}
	if overflow {
		out[len(out)-1] ^= cmacRb
	}
	return out
}
