package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/hashicorp/mdns"
)

func main() {
	host, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	instanceName := host + "-" + runtime.GOOS
	svc, err := mdns.NewMDNSService(instanceName, "_statetransfer._tcp", "", "", int(9092), nil, []string{"StateTransfer"})
	if err != nil {
		fmt.Println(err)
		panic(err)
	}

	listener, err := net.Listen("tcp", ":9092")
	if err != nil {
		fmt.Println(err)
		panic(err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Println(err)
				return
			}
			go handleConnection(conn)
		}
	}()

	server, err := mdns.NewServer(&mdns.Config{Zone: svc})
	if err != nil {
		fmt.Println(err)
		panic(err)
	}
	defer server.Shutdown()

	entries := make(chan *mdns.ServiceEntry)
	go func() {
		for {
			entry := <-entries
			fmt.Printf("Found: %s at %s:%d\n", entry.Name, entry.AddrV4, entry.Port)
			if entry.Name != instanceName+"._statetransfer._tcp.local." {
				go func(addr string) {
					conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
					if err != nil {
						fmt.Println(err)
						panic(err)
					}
					defer conn.Close()
					fmt.Fprintln(conn, "ping")
					fmt.Println("Sent ping")
					scanner := bufio.NewScanner(conn)
					if scanner.Scan() && scanner.Text() == "pong" {
						fmt.Println("Received pong")
					}
				}(fmt.Sprintf("%s:%d", entry.AddrV4, entry.Port))
			}
		}
	}()

	go func() {
		for {
			mdns.Lookup("_statetransfer._tcp", entries)
			time.Sleep(10 * time.Second)
		}
	}()
	waitForSignal()
}

func waitForSignal() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	if scanner.Scan() && scanner.Text() == "ping" {
		fmt.Fprintln(conn, "pong")
		fmt.Println("Replied pong")
	}
}
