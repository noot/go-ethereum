package crypto

import (
	"fmt"
	"errors"
	"bytes"
	"math/big"
	"crypto/rand"
	"crypto/elliptic"
	"crypto/ecdsa"

 	"golang.org/x/crypto/sha3"
	"github.com/ethereum/go-ethereum/crypto"
)

type Ring []*ecdsa.PublicKey

type RingSign struct {
	Size int // size of ring
	M []byte // message
	C *big.Int // ring signature value
	S []*big.Int // ring signature values
	Ring Ring // array of public keys
	Curve elliptic.Curve 
}

// creates a ring with size specified by `size` and places the public key corresponding to `privkey` in index 0 of the ring
// returns a new key ring of type []*ecdsa.PublicKey
func GenNewKeyRing(size int, privkey *ecdsa.PrivateKey, s int) ([]*ecdsa.PublicKey) {
	//ring := new(Ring)
	ring := make([]*ecdsa.PublicKey, size)
	pubkey := privkey.Public().(*ecdsa.PublicKey)
	ring[s] = pubkey

	for i := 1; i < size; i++ {
		idx := (i+s) % size
		priv, err := crypto.GenerateKey()
		if err != nil {
			return nil
		}

		pub := priv.Public()
		ring[idx] = pub.(*ecdsa.PublicKey)
	}

	return ring
}

// create ring signature from list of public keys given inputs:
// msg: byte array, message to be signed
// ring: array of *ecdsa.PublicKeys to be included in the ring
// privkey: *ecdsa.PrivateKey of signer
// s: index of signer in ring
func Sign(m []byte, ring []*ecdsa.PublicKey, privkey *ecdsa.PrivateKey, s int) (*RingSign, error) {
	// check ringsize > 1
	ringsize := len(ring)
	if ringsize < 2 {
		return nil, errors.New("size of ring less than two")
	} else if s >= ringsize || s < 0 {
		return nil, errors.New("secret index out of range of ring size")
	}

	// setup
	pubkey := privkey.Public().(*ecdsa.PublicKey)
	curve := pubkey.Curve
	sig := new(RingSign)
	sig.Size = ringsize
	sig.M = m
	sig.Ring = ring
	sig.Curve = curve

	// check that key at index s is indeed the signer
	if ring[s] != pubkey {
		return nil, errors.New("secret index in ring is not signer")
	}

	// start at c[1]
	// pick random scalar u (glue value), calculate c[1] = H(m, u*G) where H is a hash function and G is the base point of the curve
	C := make([]*big.Int, ringsize)
	S := make([]*big.Int, ringsize)

	// pick random scalar u
	u, err := rand.Int(rand.Reader, curve.Params().P)	
	if err != nil {
		return nil, err
	}

	// compute u*G
	ux, uy := curve.ScalarBaseMult(u.Bytes())
	// concatenate m and u*G and calculate c[1] = H(m, u*G)
	C_i := sha3.Sum256(append(m, append(ux.Bytes(), uy.Bytes()...)...))
	idx := (s+1) % ringsize
	C[idx] = new(big.Int).SetBytes(C_i[:])

	for i := 1; i < ringsize; i++ { 
		idx := (s+i) % ringsize

		// pick random scalar s_i
		s_i, err := rand.Int(rand.Reader, curve.Params().P)
		S[idx] = s_i
		if err != nil {
			return nil, err
		}	

		// calculate c[0] = H(m, s[n-1]*G + c[n-1]*P[n-1]) where n = ringsize
		px, py := curve.ScalarMult(ring[idx].X, ring[idx].Y, C[idx].Bytes()) // px, py = c[n-1]*P[n-1]
		sx, sy := curve.ScalarBaseMult(s_i.Bytes())	// sx, sy = s[n-1]*G
		tx, ty := curve.Add(sx, sy, px, py) // temp values
		C_i = sha3.Sum256(append(m, append(tx.Bytes(), ty.Bytes()...)...))

		if i == ringsize - 1 {
			C[s] = new(big.Int).SetBytes(C_i[:])
		} else {
			C[(idx+1)%ringsize] = new(big.Int).SetBytes(C_i[:])
		}
	}

	// close ring by finding s[0] = ( u - c[0]*k[0] ) mod P where P[0] = k[0]*G and P is the order of the curve
	S[s] = new(big.Int).Sub(u, new(big.Int).Mod(new(big.Int).Mul(C[s], privkey.D), curve.Params().N))

	// check that u*G = s[0]*G + c[0]*P[0]
	px, py := curve.ScalarMult(ring[s].X, ring[s].Y, C[s].Bytes())
	sx, sy := curve.ScalarBaseMult(S[s].Bytes())
	tx, ty := curve.Add(sx, sy, px, py) 

	// check that H(m, s[0]*G + c[0]*P[0]) == H(m, u*G) == C[1]
	C_i = sha3.Sum256(append(m, append(tx.Bytes(), ty.Bytes()...)...))
	C_big := new(big.Int).SetBytes(C_i[:])

	if !bytes.Equal(tx.Bytes(), ux.Bytes()) || !bytes.Equal(ty.Bytes(), uy.Bytes()) || !bytes.Equal(C[(s+1)%ringsize].Bytes(), C_big.Bytes()) {
			return nil, errors.New("error closing ring")
	}

	// everything ok, add values to signature
	sig.S = S
	sig.C = C[0]
	
	return sig, nil
}

// verify ring signature contained in RingSign struct
// returns true if a valid signature, false otherwise
func Verify(sig *RingSign) (bool, error) { 
	// setup
	ring := sig.Ring
	ringsize := sig.Size
	S := sig.S
	C := make([]*big.Int, ringsize)
	C[0] = sig.C
	curve := ring[0].Curve

	// calculate c[i+1] = H(m, s[i]*G + c[i]*P[i])
	// and c[0] = H)(m, s[n-1]*G + c[n-1]*P[n-1]) where n is the ring size
	for i := 0; i < ringsize; i++ {
		px, py := curve.ScalarMult(ring[i].X, ring[i].Y, C[i].Bytes())
		sx, sy := curve.ScalarBaseMult(S[i].Bytes())
		tx, ty := curve.Add(sx, sy, px, py)	
		C_i := sha3.Sum256(append(sig.M, append(tx.Bytes(), ty.Bytes()...)...))
		if i == ringsize - 1 {
			C[0] = new(big.Int).SetBytes(C_i[:])	
		} else {
			C[i+1] = new(big.Int).SetBytes(C_i[:])	
		}	
	}

	return bytes.Equal(sig.C.Bytes(), C[0].Bytes()), nil
}