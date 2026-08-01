package wmbus

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

// fillerByte is the 0x2F idle filler of EN 13757-3. Two of them open every
// encrypted payload, which is how a decryption is checked, and the payload is
// padded with them up to the block size.
const fillerByte = 0x2F

// decrypt turns the transport layer data into application data. Unencrypted
// telegrams pass through untouched; encrypted ones have their leading blocks
// decrypted in place of the ciphertext, with any plaintext tail kept.
func decrypt(tr *transport, key []byte) ([]byte, error) {
	if tr.mode == ModeNone || tr.encryptedBytes == 0 {
		return tr.data, nil
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("%w (security mode %d)", ErrKeyRequired, tr.mode)
	}
	if len(key) != aes.BlockSize {
		return nil, fmt.Errorf("%w, got %d", ErrInvalidKey, len(key))
	}
	if tr.encryptedBytes%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: %d encrypted bytes is not a whole number of blocks", ErrInvalidFrame, tr.encryptedBytes)
	}

	var (
		blockKey []byte
		iv       []byte
		err      error
	)
	switch tr.mode {
	case ModeAESCBCIV:
		blockKey, iv = key, modeFiveIV(tr)
	case ModeAESCBCDerived:
		blockKey, err = deriveKey(key, tr)
		if err != nil {
			return nil, err
		}
		iv = make([]byte, aes.BlockSize)
	default:
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedMode, tr.mode)
	}

	plain, err := decryptCBC(blockKey, iv, tr.data[:tr.encryptedBytes])
	if err != nil {
		return nil, err
	}
	if len(plain) < 2 || plain[0] != fillerByte || plain[1] != fillerByte {
		return nil, fmt.Errorf("%w: decrypted payload does not start with the 2F 2F marker", ErrDecrypt)
	}

	out := make([]byte, 0, len(tr.data))
	out = append(out, plain...)
	out = append(out, tr.data[tr.encryptedBytes:]...)
	return out, nil
}

// decryptCBC runs AES-128 in CBC mode over a whole number of blocks.
func decryptCBC(key, iv, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	return out, nil
}

// modeFiveIV builds the security mode 5 initialisation vector per OMS: the
// eight address bytes (manufacturer, identification number, version, device
// type) followed by the access number repeated eight times.
func modeFiveIV(tr *transport) []byte {
	iv := make([]byte, aes.BlockSize)
	copy(iv, tr.address[:])
	for i := len(tr.address); i < len(iv); i++ {
		iv[i] = tr.accessNumber
	}
	return iv
}

// keyDerivationEncrypt is the derivation constant that selects the encryption
// key of a message sent by the meter.
const keyDerivationEncrypt = 0x00

// deriveKey derives the security mode 7 message key from the master key, per
// the OMS key derivation function:
//
//	K = CMAC(masterKey, DC || C || ID || 07 07 07 07 07 07 07)
//
// DC is the derivation constant, C the four byte message counter of the
// authentication layer in transmission order, and ID the four byte
// identification number of the meter.
func deriveKey(masterKey []byte, tr *transport) ([]byte, error) {
	if !tr.hasCounter {
		return nil, fmt.Errorf("%w: security mode 7 needs the authentication layer message counter", ErrInvalidFrame)
	}
	input := make([]byte, 0, aes.BlockSize)
	input = append(input, keyDerivationEncrypt)
	input = append(input, tr.counter[:]...)
	input = append(input, tr.address[2:6]...)
	for len(input) < aes.BlockSize {
		input = append(input, 0x07)
	}
	return cmacAES(masterKey, input)
}
