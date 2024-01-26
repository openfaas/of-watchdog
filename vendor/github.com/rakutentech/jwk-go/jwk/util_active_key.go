package jwk

// ActiveKey returns the middlemost KeySpec in the KeySpecSet.
//
// The middlemost KeySpec is the safest to use when you're implementing key
// rotation and you need to choose the active key for signing or encryption.
func (ks KeySpecSet) ActiveKey() *KeySpec {
	l := len(ks.Keys)
	if l == 0 {
		return nil
	}
	middlemostPosition := (l - 1) / 2
	return &ks.Keys[middlemostPosition]
}
