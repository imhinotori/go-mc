package net

import (
	"fmt"
	"testing"
)

func Test(t *testing.T) {
	c := make(chan int, 1)
	go server(t, c)
	<-c
	client(t)
	<-c
}

// server runs in a separate goroutine, so it must not call t.Fatal (which calls
// runtime.Goexit on the wrong goroutine and does not reliably fail the test, per
// go vet's "call to (*testing.T).Fatal from a non-test goroutine" check). It uses
// t.Error + return instead. It always emits exactly two sends on c (a "prepared"
// signal and a "finished" signal) so the main test goroutine's two receives never
// deadlock, regardless of which path returns early.
func server(t *testing.T, c chan<- int) {
	prepared := false
	defer func() {
		if !prepared {
			c <- 1 // ensure the main goroutine's first <-c is satisfied
		}
		c <- 2 // finished
	}()

	l, err := ListenRCON("localhost:25575")
	if err != nil {
		t.Error(err)
		return
	}
	defer l.Close()

	c <- 1 // prepared
	prepared = true

	conn, err := l.Accept()
	if err != nil {
		t.Error(err)
		return
	}

	err = conn.AcceptLogin("RightPassword")
	if err != nil {
		t.Error("password wrong")
		return
	}

	cmd, err := conn.AcceptCmd()
	if err != nil {
		t.Log(err)
		return
	}

	resp := handleCommand(cmd)
	err = conn.RespCmd(resp)
	if err != nil {
		t.Error(err)
		return
	}
}

func handleCommand(cmd string) (resp string) {
	return fmt.Sprintf("your command is %q", cmd)
}

func client(t *testing.T) {
	conn, err := DialRCON("localhost:25575", "RightPassword")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	err = conn.Cmd("TEST COMMAND")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := conn.Resp()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Server response: %q", resp)
}

func ExampleListenRCON() {
	l, err := ListenRCON("localhost:25575")
	if err != nil {
		panic(err)
	}
	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Printf("Accept connection error: %v", err)
		}

		go func(conn RCONServerConn) {
			err = conn.AcceptLogin("CORRECT_PASSWORD")
			if err != nil {
				fmt.Printf("Login fail: %v", err)
			}
			defer conn.Close()

			// The client is login, we are accepting its command
			for {
				cmd, err := conn.AcceptCmd()
				if err != nil {
					fmt.Printf("Read command fail: %v", err)
					break
				}

				resp := handleCommand(cmd)

				// Return the result of command.
				// It's allowed to call RespCmd multiple times for one command.
				err = conn.RespCmd(resp)
				if err != nil {
					fmt.Printf("Response command fail: %v", err)
					break
				}
			}
		}(conn)
	}
}

func ExampleDialRCON() {
	conn, err := DialRCON("localhost:25575", "CORRECT_PASSWORD")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	err = conn.Cmd("TEST COMMAND")
	if err != nil {
		panic(err)
	}

	for {
		// Server may send the result in more(or less) than one packet.
		// See: https://wiki.vg/RCON#Fragmentation
		resp, err := conn.Resp()
		if err != nil {
			fmt.Print(err)
		}
		fmt.Printf("Server response: %q", resp)
		break
	}
}
