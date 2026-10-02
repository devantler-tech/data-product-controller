package main

import (
	"errors"
	"fmt"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/topology"
)

// Denial proof must reject authentication/transport failures and partially classified writes.
func TestMongoAuthorizationDenial(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{"command unauthorized", mongo.CommandError{Code: 13}, true},
		{"wrapped unauthorized", fmt.Errorf("operation: %w", mongo.CommandError{Code: 13}), true},
		{"authentication failure", mongo.CommandError{Code: 18}, false},
		{"transport failure", errors.New("network failure"), false},
		{"successful write", nil, false},
		{"unauthorized write", mongo.WriteException{WriteErrors: mongo.WriteErrors{{Code: 13}}}, true},
		{"mixed write errors", mongo.WriteException{WriteErrors: mongo.WriteErrors{{Code: 13}, {Code: 11000}}}, false},
		{"empty write errors", mongo.WriteException{}, false},
		{"write concern failure", mongo.WriteException{WriteErrors: mongo.WriteErrors{{Code: 13}}, WriteConcernError: &mongo.WriteConcernError{Code: 64}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := unauthorized(tt.err); got != tt.want {
				t.Fatalf("unauthorized = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStalePasswordRequiresAuthenticationDenial(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{"command", mongo.CommandError{Code: 18}, true},
		{"handshake", topology.ConnectionError{Wrapped: fmt.Errorf("authentication: %w", driver.Error{Code: 18})}, true},
		{"authorization", mongo.CommandError{Code: 13}, false},
		{"transport", topology.ConnectionError{Wrapped: errors.New("unreachable")}, false},
		{"success", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := authenticationFailure(tt.err); got != tt.want {
				t.Fatalf("authenticationFailure = %v, want %v", got, tt.want)
			}
		})
	}
}
