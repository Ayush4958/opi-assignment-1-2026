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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1 << 20

func startTestServer(t *testing.T, dpf nvidia.DPFClient, node string, vfs int) (*grpc.ClientConn, func()) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	plugin := vspgrpc.NewDemoVSP(node, dpf, vfs)
	srv := &vspgrpc.Server{Plugin: plugin, Node: node}
	g := grpc.NewServer()
	vspgrpc.Register(g, srv)

	go func() {
		if err := g.Serve(lis); err != nil {
			t.Logf("grpc serve: %v", err)
		}
	}()

	dial := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(dial),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn, func() {
		conn.Close()
		g.Stop()
	}
}

func TestVSPDaemon_GRPC_InitGetDevicesPing(t *testing.T) {
	ctx := context.Background()
	dpf := nvidia.NewInMemoryDPFClient()
	if err := vspgrpc.SeedDemoCluster(ctx, dpf, vspgrpc.DemoNode); err != nil {
		t.Fatalf("seed: %v", err)
	}
	conn, cleanup := startTestServer(t, dpf, vspgrpc.DemoNode, 3)
	defer cleanup()

	lc := pb.NewLifeCycleServiceClient(conn)
	dev := pb.NewDeviceServiceClient(conn)
	hb := pb.NewHeartbeatServiceClient(conn)

	ep, err := lc.Init(ctx, &pb.InitRequest{DpuIdentifier: "demo-dpu"})
	if err != nil || ep.GetPort() != 50051 {
		t.Fatalf("Init = %+v, %v", ep, err)
	}

	list, err := dev.GetDevices(ctx, &pb.Empty{})
	if err != nil || len(list.GetDevices()) != 3 {
		t.Fatalf("GetDevices = %d devices, %v", len(list.GetDevices()), err)
	}

	pong, err := hb.Ping(ctx, &pb.PingRequest{Timestamp: time.Now().Unix(), SenderId: "test"})
	if err != nil || !pong.GetHealthy() {
		t.Fatalf("Ping = %+v, %v", pong, err)
	}
}

func TestVSPDaemon_GRPC_CreateNetworkFunction(t *testing.T) {
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

	conn, cleanup := startTestServer(t, dpf, node, 4)
	defer cleanup()
	nfClient := pb.NewNetworkFunctionServiceClient(conn)

	if _, err := nfClient.CreateNetworkFunction(ctx, &pb.NFRequest{
		Input: nf.Input, Output: nf.Output, BridgeId: nf.BridgeID,
	}); err != nil {
		t.Fatalf("CreateNetworkFunction: %v", err)
	}
}

func TestVSPDaemon_GRPC_DpuModeUnimplemented(t *testing.T) {
	ctx := context.Background()
	dpf := nvidia.NewInMemoryDPFClient()
	conn, cleanup := startTestServer(t, dpf, vspgrpc.DemoNode, 2)
	defer cleanup()

	lc := pb.NewLifeCycleServiceClient(conn)
	_, err := lc.Init(ctx, &pb.InitRequest{DpuMode: true})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unimplemented {
		t.Fatalf("want UNIMPLEMENTED, got %v", err)
	}
}
