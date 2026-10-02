package main

import (
	"errors"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver"
)

// unauthorized distinguishes privilege denial from a broken connection or invalid password.
func unauthorized(err error) bool {
	var command mongo.CommandError
	if errors.As(err, &command) {
		return command.Code == 13
	}
	var write mongo.WriteException
	if !errors.As(err, &write) || write.WriteConcernError != nil || len(write.WriteErrors) == 0 {
		return false
	}
	for _, failure := range write.WriteErrors {
		if failure.Code != 13 {
			return false
		}
	}
	return true
}

func authenticationFailure(err error) bool {
	var command mongo.CommandError
	if errors.As(err, &command) {
		return command.Code == 18
	}
	var handshake driver.Error
	return errors.As(err, &handshake) && handshake.Code == 18
}
