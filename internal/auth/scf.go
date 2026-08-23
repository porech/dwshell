package auth

// scfIndexes is the fixed index list used by the client's getSCF() anti-tamper
// routine. The value it produces is sent alongside the encrypted token; the
// server rejects requests whose value does not match the token.
var scfIndexes = []int{23, 15, 71, 41, 21, 2, 12, 35, 86, 17, 8, 18, 13, 9, 26, 9, 24, 6, 11, 31}

// scfFieldName is the (obfuscated) form field carrying the checksum.
const scfFieldName = "AdgJklfeT1rtA"

// scf computes the anti-tamper checksum for a token JSON string, matching the
// browser client's getSCF(): concatenate token[i % len(token)] for each fixed
// index i.
func scf(token string) string {
	l := len(token)
	if l == 0 {
		return ""
	}
	b := make([]byte, len(scfIndexes))
	for i, idx := range scfIndexes {
		b[i] = token[idx%l]
	}
	return string(b)
}
