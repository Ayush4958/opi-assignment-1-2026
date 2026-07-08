// vspclient exercises a live vspdaemon over gRPC (reviewer demo / OPI dpu-daemon smoke).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	pb "github.com/adityasarna/opi-nvidia-vsp-skeleton/api/vsp"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "localhost:50051", "vspdaemon gRPC address")
	withNF := flag.Bool("nf", false, "also call CreateNetworkFunction (daemon must be started with -seed-nf)")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	lc := pb.NewLifeCycleServiceClient(conn)
	dev := pb.NewDeviceServiceClient(conn)
	hb := pb.NewHeartbeatServiceClient(conn)

	ep, err := lc.Init(ctx, &pb.InitRequest{DpuIdentifier: "demo-client"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Init: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Init → %s:%d\n", ep.GetIp(), ep.GetPort())

	list, err := dev.GetDevices(ctx, &pb.Empty{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "GetDevices: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("GetDevices → %d VF(s)\n", len(list.GetDevices()))
	for id, d := range list.GetDevices() {
		fmt.Printf("  %s health=%s node=%s\n", id, d.GetHealth(), d.GetTopology().GetNode())
	}

	pong, err := hb.Ping(ctx, &pb.PingRequest{Timestamp: time.Now().Unix(), SenderId: "vspclient"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ping: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Ping → healthy=%v responder=%s\n", pong.GetHealthy(), pong.GetResponderId())

	if *withNF {
		nf := pb.NewNetworkFunctionServiceClient(conn)
		req := &pb.NFRequest{
			Input:    "vf:0000:03:00.0",
			Output:   "rep:pf0vf0",
			BridgeId: "br-web",
		}
		if _, err := nf.CreateNetworkFunction(ctx, req); err != nil {
			fmt.Fprintf(os.Stderr, "CreateNetworkFunction: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("CreateNetworkFunction → OK")
	}

	fmt.Println("vspclient demo OK")
}
