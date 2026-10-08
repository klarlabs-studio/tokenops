//go:build !darwin

package keychain

func quiet(Item) (string, error) { return "", ErrUnsupported }
