package rpc

import (
	"cloud-ide/internal/rpc/middleware"
	"cloud-ide/pkg/pb"
	"context"
	"net"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
)

type GrpcServer struct {
	logger logr.Logger
	addr   string
	ccpSvc pb.CCPServiceServer
}

func New(addr string, logger logr.Logger, ccpSvc pb.CCPServiceServer) *GrpcServer {
	return &GrpcServer{
		logger: logger,
		addr:   addr,
		ccpSvc: ccpSvc,
	}
}

func (r *GrpcServer) Start(ctx context.Context) error {
	if r.addr == "" {
		r.addr = ":6387"
	}

	listener, err := net.Listen("tcp", r.addr)
	if err != nil {
		r.logger.Error(err, "create grpc service")
		return err
	}
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(
		middleware.RecoveryInterceptorMiddleware(&r.logger),
	))
	pb.RegisterCCPServiceServer(server, r.ccpSvc)

	go func() {
		<-ctx.Done()
		server.GracefulStop()
	}()

	r.logger.Info("grpc server listen", "addr", r.addr)
	if err := server.Serve(listener); err != nil {
		r.logger.Error(err, "start grpc server")
		return err
	}

	r.logger.Info("grpc server stopped")

	return nil
}
