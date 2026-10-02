package main

import (
	"errors"
	"net"
)

func networkDenial(err error) bool {
	var failure net.Error
	return errors.As(err, &failure) && failure.Timeout()
}
