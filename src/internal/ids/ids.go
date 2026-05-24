package ids

import (
	"crypto/rand"
	"encoding/base32"
)

const (
	PrefixCompany = "co_"
	PrefixList    = "l_"
	PrefixContact = "c_"
	PrefixSend    = "s_"
	PrefixBatch   = "b_"
	PrefixUnsub   = "u_"
)

var enc = base32.StdEncoding.WithPadding(base32.NoPadding)

func New(prefix string) string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("rand.Read: " + err.Error())
	}
	return prefix + enc.EncodeToString(b[:])[:10]
}

func NewCompany() string { return New(PrefixCompany) }
func NewList() string    { return New(PrefixList) }
func NewContact() string { return New(PrefixContact) }
func NewSend() string    { return New(PrefixSend) }
func NewBatch() string   { return New(PrefixBatch) }
func NewUnsub() string   { return New(PrefixUnsub) }
