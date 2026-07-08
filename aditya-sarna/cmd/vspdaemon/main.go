package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	nvidia "github.com/adityasarna/opi-nvidia-vsp-skeleton"
	"github.com/adityasarna/opi-nvidia-vsp-skeleton/vspgrpc"
)

func main() {
	addr := flag.String("addr", ":50051", "gRPC listen address")
	node := flag.String("node", vspgrpc.DemoNode, "Kubernetes node name this VSP serves")
	vfCount := flag.Int("vfs", 4, "VF count reported by demo enumerator")
	seedNF := flag.Bool("seed-nf", false, "pre-seed demo NF chain Ready for CreateNetworkFunction demos")
	flag.Parse()

	dpf := nvidia.NewInMemoryDPFClient()
	ctx := context.Background()
	if err := vspgrpc.SeedDemoCluster(ctx, dpf, *node); err != nil {
		fmt.Fprintf(os.Stderr, "seed cluster: %v\n", err)
		os.Exit(1)
	}
	if *seedNF {
		nf := nvidia.NFRequest{Input: "vf:0000:03:00.0", Output: "rep:pf0vf0", BridgeID: "br-web"}
		if err := vspgrpc.SeedNFReady(ctx, dpf, *node, nf); err != nil {
			fmt.Fprintf(os.Stderr, "seed nf: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("seed-nf: demo chain pre-marked Ready (CreateNetworkFunction will succeed)")
	}

	plugin := vspgrpc.NewDemoVSP(*node, dpf, *vfCount)
	srv := &vspgrpc.Server{Plugin: plugin, Node: *node}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Println("\nvspdaemon shutdown")
		os.Exit(0)
	}()

	if err := vspgrpc.ListenAndServe(*addr, srv); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}
