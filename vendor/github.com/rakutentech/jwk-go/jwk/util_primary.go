package jwk

import (
	"crypto/ecdsa"

	"github.com/rakutentech/jwk-go/okp"
)

// PrimaryKey returns the first KeySpec in the KeySpecSet which matches the
// specified key type and curve.
//
// If keyType contains a slash, then it will be parsed as a pair of key type
// ('kty') and curve ('crv'), in the following format: 'kty/crv', and  the
// KeySpec will match if both its key type and curve match. Otherwise, only
// the key type will be checked.
func (ks KeySpecSet) PrimaryKey(keyType string) *KeySpec {
	for _, k := range ks.Keys {
		if k.IsKeyType(keyType) {
			return &k
		}
	}
	return nil
}

// PrimaryCurveOKP returns the first CurveOctetKeyPair in the KeySpecSet
// which matches the specified curve name.
//
// If keyType contains a slash, then it will be parsed as a pair of key type
// ('kty') and curve ('crv'), in the following format: 'kty/crv', and  the
// KeySpec will match if both its key type and curve match. Otherwise, only
// the key type will be checked.
func (ks KeySpecSet) PrimaryCurveOKP(curve string) (*KeySpec, okp.CurveOctetKeyPair) {
	for _, k := range ks.Keys {
		curveOKP := k.CoerceOkpCurve(curve)
		if curveOKP != nil {
			return &k, curveOKP
		}
	}
	return nil, nil
}

// PrimaryECDSAPrivate returns the first ECDSA PrivateKey in the KeySpecSet
// which matches the specified curve name.
func (ks KeySpecSet) PrimaryECDSAPrivate() (*KeySpec, *ecdsa.PrivateKey) {
	for _, k := range ks.Keys {
		if ecdsaPrivateKey, ok := k.Key.(*ecdsa.PrivateKey); ok {
			return &k, ecdsaPrivateKey
		}
	}
	return nil, nil
}
