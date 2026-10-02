package main

import (
	"errors"
	"net"
)

func networkDenial(err error) bool {
	var failure *net.OpError
	var dnsFailure *net.DNSError
	return errors.As(err, &failure) && failure.Op == "dial" && failure.Net == "tcp" &&
		!errors.As(err, &dnsFailure) && failure.Timeout()
}
