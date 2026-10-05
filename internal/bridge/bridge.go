// Package bridge gives the aviatotest harness access to the plugin's dispatch methods without
// making them part of the public API.
package bridge

// Dispatcher returns the dispatcher of an *aviato.Plugin. Package aviato sets it in its init;
// aviatotest asserts the result to an interface listing the dispatch methods.
var Dispatcher func(plugin any) any
