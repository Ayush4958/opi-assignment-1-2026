// Package vspgrpc exposes the OPI Vendor gRPC contract over real protobuf
// stubs, delegating to nvidiadpf.NvidiaVSP (architecture_design.md §6.2).
package vspgrpc

import (
	"context"
	"errors"
	"fmt"
	"net"

	pb "github.com/adityasarna/opi-nvidia-vsp-skeleton/api/vsp"
	nvidia "github.com/adityasarna/opi-nvidia-vsp-skeleton"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Plugin is the VSP surface the gRPC layer delegates to.
type Plugin interface {
	nvidia.LifeCycleService
	nvidia.DeviceService
	nvidia.NetworkFunctionService
	nvidia.DpuNetworkConfigService
	nvidia.HeartbeatService
}

// Server implements all OPI Vendor gRPC services on one type.
type Server struct {
	pb.UnimplementedLifeCycleServiceServer
	pb.UnimplementedDeviceServiceServer
	pb.UnimplementedNetworkFunctionServiceServer
	pb.UnimplementedDpuNetworkConfigServiceServer
	pb.UnimplementedHeartbeatServiceServer

	Plugin Plugin
	Node   string
}

// Register mounts all Vendor services on g.
func Register(g *grpc.Server, srv *Server) {
	pb.RegisterLifeCycleServiceServer(g, srv)
	pb.RegisterDeviceServiceServer(g, srv)
	pb.RegisterNetworkFunctionServiceServer(g, srv)
	pb.RegisterDpuNetworkConfigServiceServer(g, srv)
	pb.RegisterHeartbeatServiceServer(g, srv)
}

// ListenAndServe starts a blocking gRPC server on addr.
func ListenAndServe(addr string, srv *Server) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	g := grpc.NewServer()
	Register(g, srv)
	fmt.Printf("vspdaemon listening on %s (node=%s)\n", addr, srv.Node)
	return g.Serve(lis)
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, nvidia.ErrUnavailable):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, nvidia.ErrFailedPrecondition):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, nvidia.ErrUnimplemented):
		return status.Error(codes.Unimplemented, err.Error())
	default:
		return status.Errorf(codes.Internal, "%v", err)
	}
}

func (s *Server) Init(ctx context.Context, req *pb.InitRequest) (*pb.IpPort, error) {
	ep, err := s.Plugin.Init(ctx, nvidia.InitRequest{
		DpuMode:       req.GetDpuMode(),
		DpuIdentifier: req.GetDpuIdentifier(),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &pb.IpPort{Ip: ep.IP, Port: ep.Port}, nil
}

func (s *Server) GetDevices(ctx context.Context, _ *pb.Empty) (*pb.DeviceListResponse, error) {
	resp, err := s.Plugin.GetDevices(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := &pb.DeviceListResponse{Devices: make(map[string]*pb.Device, len(resp.Devices))}
	for id, d := range resp.Devices {
		out.Devices[id] = &pb.Device{
			Id:     d.ID,
			Health: d.Health,
			Topology: &pb.TopologyInfo{
				Node: d.Topology.Node,
			},
		}
	}
	return out, nil
}

func (s *Server) SetNumVfs(ctx context.Context, req *pb.VfCount) (*pb.VfCount, error) {
	got, err := s.Plugin.SetNumVfs(ctx, nvidia.VfCount{VfCnt: req.GetVfCnt()})
	if err != nil {
		return nil, mapErr(err)
	}
	return &pb.VfCount{VfCnt: got.VfCnt}, nil
}

func (s *Server) CreateNetworkFunction(ctx context.Context, req *pb.NFRequest) (*pb.Empty, error) {
	err := s.Plugin.CreateNetworkFunction(ctx, nvidia.NFRequest{
		Input:    req.GetInput(),
		Output:   req.GetOutput(),
		BridgeID: req.GetBridgeId(),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &pb.Empty{}, nil
}

func (s *Server) DeleteNetworkFunction(ctx context.Context, req *pb.NFRequest) (*pb.Empty, error) {
	err := s.Plugin.DeleteNetworkFunction(ctx, nvidia.NFRequest{
		Input:    req.GetInput(),
		Output:   req.GetOutput(),
		BridgeID: req.GetBridgeId(),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &pb.Empty{}, nil
}

func (s *Server) SetDpuNetworkConfig(ctx context.Context, req *pb.DpuNetworkConfigRequest) (*pb.Empty, error) {
	err := s.Plugin.SetDpuNetworkConfig(ctx, nvidia.DpuNetworkConfigRequest{
		IsAccelerated: req.GetIsAccelerated(),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &pb.Empty{}, nil
}

func (s *Server) Ping(ctx context.Context, req *pb.PingRequest) (*pb.PingResponse, error) {
	resp, err := s.Plugin.Ping(ctx, nvidia.PingRequest{
		Timestamp: req.GetTimestamp(),
		SenderID:  req.GetSenderId(),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return &pb.PingResponse{
		Timestamp:   resp.Timestamp,
		ResponderId: resp.ResponderID,
		Healthy:     resp.Healthy,
	}, nil
}
