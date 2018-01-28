/*
*    Go Network Monitor - personal project
*    Released under the MIT License:  https://gherlein.mit-license.org/
 */

package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/onsi/gocleanup"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type node struct {
	hostname string
	addr     string
	IP       net.IP
	incount  uint64
	outcount uint64
}

var (
	device      string = ""
	cidr        string = ""
	snapshotLen int32  = 1024
	promiscuous bool   = true
	err         error
	timeout     time.Duration = 30 * time.Second
	handle      *pcap.Handle
	nodes       []node
	debug       bool = false
	netBytes         = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "network_bytes_total",
		Help: "Number of bytes seen on the network.",
	})
	nodeBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "node_bytes_total",
			Help: "Number of bytes seen on that network node.",
		},
		[]string{"device"},
	)
)

func init() {
	flag.StringVar(&device, "device", "", "name of the network device to monitor")
	flag.StringVar(&cidr, "cidr", "", "CIDR of the network device to monitor")
	prometheus.MustRegister(netBytes)
	prometheus.MustRegister(nodeBytes)
}

func calcNetwork(d string, c string) (int, string, error) {
	var (
		mask     net.IPMask
		masklen  int
		numhosts int
		baseaddr string
	)
	ipv4Addr, ipv4Net, err := net.ParseCIDR(c)
	if err != nil {
		log.Fatal(err)
		return 0, "", err
	}

	mask = ipv4Addr.DefaultMask()
	masklen, _ = mask.Size()
	numhosts = int(math.Pow(2, float64(32-masklen)))
	baseaddr = strings.TrimSuffix(ipv4Addr.String(), ".0")

	if debug {
		fmt.Println(ipv4Addr)
		fmt.Println(ipv4Net)
		fmt.Println(numhosts)
		fmt.Println(baseaddr)
		fmt.Println(c)
	}
	return numhosts, baseaddr, nil
}

func main() {

	var (
		numhosts int
		baseaddr string
	)

	flag.Parse()
	if device == "" || cidr == "" {
		fmt.Printf("device name and/or cidr not specified\n")
		os.Exit(3)
	}
	numhosts, baseaddr, err = calcNetwork(device, cidr)
	fmt.Printf("Device: %s - CIDR: %s - numhosts: %d - baseaddr: %s\n", device, cidr, numhosts, baseaddr)

	// Open device
	handle, err = pcap.OpenLive(device, snapshotLen, promiscuous, timeout)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	gocleanup.Register(printStats)

	for x := 0; x < numhosts; x++ {
		if x == 0 {
			continue
		}
		addr := fmt.Sprintf("%s.%d", baseaddr, x)
		names, err := net.LookupAddr(addr)
		var hostname string
		if err != nil || len(names) == 0 {
			hostname = "unknown"
			if debug {
				fmt.Printf("%s - %s\n", addr, hostname)
			}
		} else {
			hostname = names[0]
			if debug {
				fmt.Printf("%s - %s\n", addr, hostname)
			}
		}

		nodes = append(nodes,
			node{IP: net.ParseIP(addr),
				addr:     addr,
				hostname: hostname,
				incount:  0,
				outcount: 0,
			})
	}

	if debug {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Press any key to continue...")
		text, _ := reader.ReadString('\n')
		fmt.Println(text)
	}

	ticker := time.NewTicker(time.Minute)
	go func() {
		for t := range ticker.C {
			fmt.Println("Time:", t)
			printStats()
			clearStats()
		}
	}()

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe(":8080", nil))
	}()

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	for packet := range packetSource.Packets() {
		//printPacketInfo(packet)
		analyzePacket(packet)
	}

}

func analyzePacket(packet gopacket.Packet) {

	ipLayer := packet.Layer(layers.LayerTypeIPv4)
	if ipLayer != nil {
		ip, _ := ipLayer.(*layers.IPv4)
		netBytes.Add(float64(ip.Length))
		for i, node := range nodes {
			if node.addr != "" {
				if node.IP.Equal(ip.SrcIP) {
					nodeBytes.With(prometheus.Labels{"device": node.hostname}).Add(float64(ip.Length))
					nodes[i].outcount += uint64(ip.Length)
					if debug {
						fmt.Printf("From %s to %s - len %d\n", node.hostname, ip.DstIP, ip.Length)
					}
				}
				if node.IP.Equal(ip.DstIP) {
					nodeBytes.With(prometheus.Labels{"device": node.hostname}).Add(float64(ip.Length))
					nodes[i].incount += uint64(ip.Length)
					if debug {
						fmt.Printf("From %s to %s - len %d\n", ip.SrcIP, node.hostname, ip.Length)
					}
				}
			}

		}
	}
}

func printStats() {
	fmt.Printf("------------------- Summary Stats ------------------- \n")
	for _, node := range nodes {
		if node.incount != 0 && node.outcount != 0 {
			fmt.Printf("%-16s   %-30s    %-10.1fk    %-10.1fk\n",
				node.addr,
				node.hostname,
				float64(node.incount)/1000,
				float64(node.outcount)/1000)
		}
	}
}

func clearStats() {
	for i, _ := range nodes {
		nodes[i].incount = 0
		nodes[i].outcount = 0
	}
}
