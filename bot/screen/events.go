package screen

import "github.com/imhinotori/sulfur/chat"

type EventsListener struct {
	Open    func(id int, container_type int32, title chat.Message) error
	SetSlot func(id, index int) error
	Close   func(id int) error
}
