package main

import (
	"log"
	"net"
	"os"
	"os/signal"

	"github.com/oleksandr/bonjour"
)

func getLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip != nil && !ip.IsLoopback() {
			return ip.String(), nil
		}
	}
	return "", nil
}

func main() {
	localIP, err := getLocalIP()
	if err != nil {
		log.Fatalln(err.Error())
	}

	// Register nodecrane service
	s1, err := bonjour.RegisterProxy("frontend", "_cloudwrench._tcp", "", 80, "cloudwrench", localIP, []string{"txtv=1", "app=test"}, nil)
	if err != nil {
		log.Fatalln(err.Error())
	}

	// Register api.nodecrane service
	s2, err := bonjour.RegisterProxy("backend", "_api.cloudwrench._tcp", "", 80, "api.cloudwrench", localIP, []string{"txtv=1", "app=test"}, nil)
	if err != nil {
		log.Fatalln(err.Error())
	}

	// Ctrl+C handling
	handler := make(chan os.Signal, 1)
	signal.Notify(handler, os.Interrupt)

	for sig := range handler {
		if sig == os.Interrupt {
			log.Println("Shutting down...")
			s1.Shutdown()
			s2.Shutdown()

			break
		}
	}
}
