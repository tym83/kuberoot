// Command plc-sim plays a PLC for trying the iot distribution: a Modbus TCP
// server whose holding registers and coils start at the values given and
// take writes. Usage:
//
//	plc-sim serve -listen tcp://0.0.0.0:5020 -holding 0=700 -coil 0=1
//	plc-sim set -addr tcp://plc:5020 -holding 0=853
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/simonvetter/modbus"
)

type plc struct {
	mu      sync.Mutex
	holding map[uint16]uint16
	coils   map[uint16]bool
}

func (p *plc) HandleCoils(req *modbus.CoilsRequest) ([]bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]bool, req.Quantity)
	for i := range out {
		a := req.Addr + uint16(i)
		if req.IsWrite {
			p.coils[a] = req.Args[i]
		}
		out[i] = p.coils[a]
	}
	return out, nil
}

func (p *plc) HandleDiscreteInputs(req *modbus.DiscreteInputsRequest) ([]bool, error) {
	return make([]bool, req.Quantity), nil
}

func (p *plc) HandleHoldingRegisters(req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]uint16, req.Quantity)
	for i := range out {
		a := req.Addr + uint16(i)
		if req.IsWrite {
			p.holding[a] = req.Args[i]
			log.Printf("holding %d = %d", a, req.Args[i])
		}
		out[i] = p.holding[a]
	}
	return out, nil
}

func (p *plc) HandleInputRegisters(req *modbus.InputRegistersRequest) ([]uint16, error) {
	return make([]uint16, req.Quantity), nil
}

// pairs reads "address=value" flags.
type pairs map[uint16]int64

func (p pairs) String() string { return fmt.Sprint(map[uint16]int64(p)) }
func (p pairs) Set(s string) error {
	a, v, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("%q: address=value", s)
	}
	addr, err := strconv.ParseUint(a, 10, 16)
	if err != nil {
		return err
	}
	val, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return err
	}
	p[uint16(addr)] = val
	return nil
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: plc-sim serve|set ...")
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	holding, coils := pairs{}, pairs{}
	fs.Var(holding, "holding", "holding register address=value, repeated")
	fs.Var(coils, "coil", "coil address=0|1, repeated")
	listen := fs.String("listen", "tcp://0.0.0.0:5020", "where to serve")
	addr := fs.String("addr", "tcp://127.0.0.1:5020", "the PLC to write to")
	_ = fs.Parse(os.Args[2:])
	switch os.Args[1] {
	case "serve":
		p := &plc{holding: map[uint16]uint16{}, coils: map[uint16]bool{}}
		for a, v := range holding {
			p.holding[a] = uint16(v)
		}
		for a, v := range coils {
			p.coils[a] = v != 0
		}
		srv, err := modbus.NewServer(&modbus.ServerConfiguration{URL: *listen, Timeout: 5 * time.Minute, MaxClients: 16,
			Logger: log.New(io.Discard, "", 0)}, p)
		if err != nil {
			log.Fatal(err)
		}
		if err := srv.Start(); err != nil {
			log.Fatal(err)
		}
		log.Printf("serving %s: holding %v, coils %v", *listen, holding, coils)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		_ = srv.Stop()
	case "set":
		c, err := modbus.NewClient(&modbus.ClientConfiguration{URL: *addr, Timeout: 3 * time.Second})
		if err != nil {
			log.Fatal(err)
		}
		if err := c.Open(); err != nil {
			log.Fatal(err)
		}
		defer c.Close()
		for a, v := range holding {
			if err := c.WriteRegister(a, uint16(v)); err != nil {
				log.Fatal(err)
			}
		}
		for a, v := range coils {
			if err := c.WriteCoil(a, v != 0); err != nil {
				log.Fatal(err)
			}
		}
	default:
		log.Fatal("usage: plc-sim serve|set ...")
	}
}
