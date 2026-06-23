// blockstate.go contains helper types for the BlockState data component.
package component

import pk "github.com/imhinotori/go-mc/net/packet"

type BlockStateProperty struct {
	Name  pk.String
	Value pk.String
}
