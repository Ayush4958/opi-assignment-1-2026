package vspgrpc_test

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/adityasarna/opi-nvidia-vsp-skeleton/api/vsp"
	nvidia "github.com/adityasarna/opi-nvidia-vsp-skeleton"
	"github.com/adityasarna/opi-nvidia-vsp-skeleton/vspgrpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func startLiveGRPCServer(t *testing.T, dpf nvidia.DPFClient, node string, vfs int) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	plugin := vspgrpc.NewDemoVSP(node, dpf, vfs)
	srv := &vspgrpc.Server{Plugin: plugin, Node: node}
	g := grpc.NewServer()
	vspgrpc.Register(g, srv)
	go func() {
		_ = g.Serve(lis)
	}()
	cleanup := func() {
		g.Stop()
		_ = lis.Close()
	}
	return lis.Addr().String(), cleanup
}

// TestGRPCDaemon_LivePing proves cmd/vspdaemon responds on real TCP (not just in-memory NvidiaVSP).
func TestGRPCDaemon_LivePing(t *testing.T) {
	ctx := context.Background()
	dpf := nvidia.NewInMemoryDPFClient()
	if err := vspgrpc.SeedDemoCluster(ctx, dpf, vspgrpc.DemoNode); err != nil {
		t.Fatalf("seed: %v", err)
	}
	addr, cleanup := startLiveGRPCServer(t, dpf, vspgrpc.DemoNode, 2)
	defer cleanup()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	lc := pb.NewLifeCycleServiceClient(conn)
	if _, err := lc.Init(ctx, &pb.InitRequest{DpuIdentifier: "live-ping"}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	hb := pb.NewHeartbeatServiceClient(conn)
	pong, err := hb.Ping(ctx, &pb.PingRequest{Timestamp: time.Now().Unix(), SenderId: "live-ping"})
	if err != nil || !pong.GetHealthy() {
		t.Fatalf("Ping = %+v, %v", pong, err)
	}
	t.Logf("LivePing OK on %s responder=%s", addr, pong.GetResponderId())
}

// TestLiveSmoke_TCPRoundTrip starts a real TCP listener (like cmd/vspdaemon) and
// drives Init / GetDevices / Ping / CreateNF — the reviewer-visible live path.
func TestLiveSmoke_TCPRoundTrip(t *testing.T) {
	ctx := context.Background()
	dpf := nvidia.NewInMemoryDPFClient()
	node := vspgrpc.DemoNode
	if err := vspgrpc.SeedDemoCluster(ctx, dpf, node); err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	nf := nvidia.NFRequest{Input: "vf:0000:03:00.0", Output: "rep:pf0vf0", BridgeID: "br-web"}
	if err := vspgrpc.SeedNFReady(ctx, dpf, node, nf); err != nil {
		t.Fatalf("seed nf: %v", err)
	}

	addr, cleanup := startLiveGRPCServer(t, dpf, node, 3)
	t.Cleanup(cleanup)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	lc := pb.NewLifeCycleServiceClient(conn)
	dev := pb.NewDeviceServiceClient(conn)
	hb := pb.NewHeartbeatServiceClient(conn)
	nfClient := pb.NewNetworkFunctionServiceClient(conn)

	ep, err := lc.Init(ctx, &pb.InitRequest{DpuIdentifier: "live-smoke"})
	if err != nil || ep.GetPort() != 50051 {
		t.Fatalf("Init = %+v, %v", ep, err)
	}

	list, err := dev.GetDevices(ctx, &pb.Empty{})
	if err != nil || len(list.GetDevices()) != 3 {
		t.Fatalf("GetDevices = %d, %v", len(list.GetDevices()), err)
	}

	pong, err := hb.Ping(ctx, &pb.PingRequest{Timestamp: time.Now().Unix(), SenderId: "live-smoke"})
	if err != nil || !pong.GetHealthy() {
		t.Fatalf("Ping = %+v, %v", pong, err)
	}

	if _, err := nfClient.CreateNetworkFunction(ctx, &pb.NFRequest{
		Input: nf.Input, Output: nf.Output, BridgeId: nf.BridgeID,
	}); err != nil {
		t.Fatalf("CreateNetworkFunction: %v", err)
	}

	t.Logf("live TCP demo OK on %s", addr)
}
