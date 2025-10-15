package rvideo

import (
	"crypto/ed25519"
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
)

func genKey(t *testing.T) {
	pubkey, prikey, err := ed25519.GenerateKey(nil)

	fmt.Printf("pub=%s,\n pri=%s,\n err=%s\n", string(pubkey), string(prikey), err.Error())
	require.NoError(t, err)
}
