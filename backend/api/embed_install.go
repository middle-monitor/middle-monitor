package api

import (
	_ "embed"
)

//go:embed embed/install.sh
var installScript []byte

// The update script is embedded for the same reason as the install script: the
// runtime images carry only the agent's built binaries and its schemas, so a
// script read from the agent tree at request time is a permanent 404 there.
//
//go:embed embed/update.sh
var updateScript []byte
