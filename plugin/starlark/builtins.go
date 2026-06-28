package starlark

import "go.starlark.net/starlark"

// echoBuiltin is the server-NEUTRAL demo builtin: it returns its single string
// argument unchanged, proving the Go<->Starlark bridge without touching game
// state. The callback signature is the verified NewBuiltin shape.
func echoBuiltin(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s starlark.String
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &s); err != nil {
		return nil, err
	}
	return s, nil
}
