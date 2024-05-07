package utils

import (
	"crypto/rand"
	"net/mail"
)

// ValidateEmail validates whether the given string is a correctly formed e-mail address.
func ValidateEmail(email string) bool {
	// Since `mail.ParseAddress` parses an email address which can also contain an optional name component,
	// here we check if incoming email string is same as the parsed email.Address. So this eliminates
	// any valid email address with name and also valid address with empty name like `<abc@example.com>`.
	em, err := mail.ParseAddress(email)
	if err != nil || em.Address != email {
		return false
	}

	return true
}

// GenerateRandomString generates a cryptographically random, alphanumeric string of length n.
func GenerateRandomString(n int) (string, error) {
	const dictionary = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var bytes = make([]byte, n)

	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	for k, v := range bytes {
		bytes[k] = dictionary[v%byte(len(dictionary))]
	}

	return string(bytes), nil
}