// writablebookcontent.go contains helper types for the WritableBookContent data component.
package component

import pk "github.com/imhinotori/go-mc/net/packet"

type Page struct {
	Raw      pk.String
	Filtered pk.Option[pk.String, *pk.String]
}
