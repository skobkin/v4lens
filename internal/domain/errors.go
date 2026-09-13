package domain

import "errors"

// ErrBusy marks a conflict with another owner of the device (e.g. an
// external application streaming while we try to reconfigure capture).
// Backends wrap concrete errors (such as EBUSY) with this sentinel; busy is
// only ever derived from operation results (ARCHITECTURE §9).
var ErrBusy = errors.New("device busy")

// ErrDisconnected marks that the device disappeared mid-operation.
var ErrDisconnected = errors.New("device disconnected")

// ErrUnsupported marks an operation the device or backend cannot perform
// (e.g. streaming an MPLANE-only capture device in the MVP).
var ErrUnsupported = errors.New("unsupported")
