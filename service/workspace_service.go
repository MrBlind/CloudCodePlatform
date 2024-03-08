package service

import (
	v1 "cloud-ide/api/v1"
	"cloud-ide/pkg/pb"
	"fmt"
	"github.com/go-logr/logr"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v12 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"sync"
	"time"
)

type WorkSpaceService struct {
	//pb.UnimplementedCCPServiceServer
	client    client.Client
	logger    logr.Logger
	namespace string
	//sid到chan的映射
	mux sync.Mutex
	wsc map[string]chan struct{}
}

func NewWorkSpaceService(c client.Client, logger logr.Logger) *WorkSpaceService {
	return &WorkSpaceService{
		client:    c,
		logger:    logger,
		wsc:       make(map[string]chan struct{}),
		namespace: "cloud-ide",
	}
}

var _ = pb.CCPServiceServer(&WorkSpaceService{})

const (
	PodNotExist int32 = iota
	PodExist
)

var (
	EmptyResponse             = &pb.Response{}
	EmptyWorkspaceRunningInfo = &pb.WorkspaceRunningInfo{}
	EmptyWorkspaceStatus      = &pb.WorkspaceStatus{}
)

var WorkspaceNotExist = "work space not exist"
var WorkspaceAlreadyExist = "workspace already exist"
var WorkspaceStartFailed = "start workspace error"
var WorkspaceNotRunning = "workspace is not running"

func (s *WorkSpaceService) CreateSpace(ctx context.Context, info *pb.WorkspaceInfo) (*pb.WorkspaceRunningInfo, error) {
	var wp v1.WorkSpace
	exist := s.checkWorkspaceExist(ctx, client.ObjectKey{Name: getWorkspaceName(info), Namespace: s.namespace}, &wp)
	sta := status.New(codes.AlreadyExists, WorkspaceAlreadyExist)
	if exist {
		return EmptyWorkspaceRunningInfo, sta.Err()
	}
	fmt.Println("print space sid:", info.Sid)

	w := s.constructWorkspace(info)
	fmt.Println("print sid:", w.Spec.SID)
	if err := s.client.Create(ctx, w); err != nil {
		if errors.IsAlreadyExists(err) {
			return EmptyWorkspaceRunningInfo, sta.Err()
		}

		s.logger.Error(err, "create workspace failed")
		return EmptyWorkspaceRunningInfo, err

	}
	return s.waitForPodRunning(ctx, client.ObjectKey{Name: w.Name, Namespace: w.Namespace}, w)
}

func (s *WorkSpaceService) StartSpace(ctx context.Context, info *pb.WorkspaceInfo) (*pb.WorkspaceRunningInfo, error) {
	var wp v1.WorkSpace
	key := client.ObjectKey{Name: getWorkspaceName(info), Namespace: s.namespace}
	exist := s.checkWorkspaceExist(ctx, key, &wp)
	if !exist {
		return EmptyWorkspaceRunningInfo, status.Error(codes.NotFound, WorkspaceNotExist)
	}

	pod := v12.Pod{}
	if err := s.client.Get(context.Background(), key, &pod); err == nil {
		return &pb.WorkspaceRunningInfo{
			NodeName: pod.Spec.NodeName,
			Ip:       pod.Status.PodIP,
			Port:     pod.Spec.Containers[0].Ports[0].ContainerPort,
		}, nil
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var p v1.WorkSpace
		exist := s.checkWorkspaceExist(ctx, key, &p)
		if !exist {
			return nil
		}

		wp.Spec.Operation = v1.WorkSpaceStart
		if err := s.client.Update(ctx, &wp); err != nil {
			klog.Errorf("update workspace to start error:%v", err)
			return err
		}
		return nil
	})
	if err != nil {
		return EmptyWorkspaceRunningInfo, status.Error(codes.Unknown, err.Error())
	}

	if !exist {
		return EmptyWorkspaceRunningInfo, status.Error(codes.NotFound, WorkspaceNotExist)
	}

	return s.waitForPodRunning(ctx, key, &wp)
}

func (s *WorkSpaceService) DeleteSpace(ctx context.Context, option *pb.QueryOption) (*pb.Response, error) {
	var wp v1.WorkSpace
	exist := s.checkWorkspaceExist(ctx, client.ObjectKey{Name: option.Name, Namespace: option.Namespace}, &wp)
	if !exist {
		return EmptyResponse, nil
	}

	if err := s.client.Delete(ctx, &wp); err != nil {
		klog.Errorf("delete workspace error:%v", err)
		return EmptyResponse, status.Error(codes.Unknown, err.Error())
	}

	return EmptyResponse, nil
}

func (s *WorkSpaceService) StopSpace(ctx context.Context, option *pb.QueryOption) (*pb.Response, error) {
	exist := true
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var wp v1.WorkSpace
		exist = s.checkWorkspaceExist(ctx, client.ObjectKey{Name: option.Name, Namespace: option.Namespace}, &wp)
		if !exist {
			return nil
		}

		wp.Spec.Operation = v1.WorkSpaceStop
		if err := s.client.Update(ctx, &wp); err != nil {
			klog.Errorf("update workspace to start error:%v", err)
			return err
		}

		return nil
	})

	if err != nil {
		return EmptyResponse, status.Error(codes.Unknown, err.Error())
	}

	if !exist {
		return EmptyResponse, status.Error(codes.NotFound, WorkspaceNotExist)
	}

	return EmptyResponse, nil
}

func (s *WorkSpaceService) GetPodSpaceStatus(ctx context.Context, option *pb.QueryOption) (*pb.WorkspaceStatus, error) {
	pod := v12.Pod{}
	err := s.client.Get(ctx, client.ObjectKey{Name: option.Name, Namespace: option.Namespace}, &pod)
	if err != nil {
		if errors.IsNotFound(err) {
			return EmptyWorkspaceStatus, status.Error(codes.NotFound, WorkspaceNotRunning)
		}
		klog.Errorf("get pod status error: %v", err)
		return &pb.WorkspaceStatus{Status: PodNotExist, Message: WorkspaceNotExist}, status.Error(codes.NotFound, err.Error())
	}

	return &pb.WorkspaceStatus{Status: PodNotExist, Message: string(pod.Status.Phase)}, nil
}

func (s *WorkSpaceService) GetPodSpaceInfo(ctx context.Context, option *pb.QueryOption) (*pb.WorkspaceRunningInfo, error) {
	pod := &v12.Pod{}
	err := s.client.Get(ctx, client.ObjectKey{Name: option.Name, Namespace: option.Namespace}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, status.Error(codes.NotFound, WorkspaceNotRunning)
		}
		klog.Errorf("get pod space info error:%v", err)
		return EmptyWorkspaceRunningInfo, status.Error(codes.Unknown, err.Error())
	}

	return &pb.WorkspaceRunningInfo{
		NodeName: pod.Spec.NodeName,
		Ip:       pod.Status.PodIP,
		Port:     pod.Spec.Containers[0].Ports[0].ContainerPort,
	}, nil
}

func (s *WorkSpaceService) GetRunningWorkspaces(ctx context.Context, req *pb.RunningSpacesRequest) (*pb.RunningSpacesList, error) {
	res := &pb.RunningSpacesList{}
	var wks v1.WorkSpaceList
	fmt.Println("11111111111111")
	fmt.Println("req:", req)
	err := s.client.List(ctx, &wks, client.MatchingLabels{"uid": req.Uid})
	if err != nil {
		klog.Error(err, "list all workspaces of user:", req.Uid)
		return res, status.Error(codes.Unknown, err.Error())
	}

	for _, item := range wks.Items {
		if item.Status.Phase == v1.WorkspacePhaseStarting || item.Status.Phase == v1.WorkspacePhaseRunning {
			res.Workspaces = append(res.Workspaces, &pb.RunningSpacesList_WorkspaceBasicInfo{
				Sid:  item.Spec.SID,
				Name: item.Name,
			})
		}
	}
	fmt.Println("result:", res)
	return res, nil
}

func (s *WorkSpaceService) constructWorkspace(space *pb.WorkspaceInfo) *v1.WorkSpace {
	hardware := fmt.Sprintf("%sC%s%s", space.Resourcelimit.Cpu,
		strings.Split(space.Resourcelimit.Memory, "i")[0], strings.Split(space.Resourcelimit.Storage, "i")[0])
	return &v1.WorkSpace{
		TypeMeta: metav1.TypeMeta{
			Kind:       "WorkSpace",
			APIVersion: "ccp.blind/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      getWorkspaceName(space),
			Namespace: s.namespace,
			Labels: map[string]string{
				"sid": space.Sid,
				"uid": space.Uid,
			},
		},
		Spec: v1.WorkSpaceSpec{
			UID:       space.Uid,
			SID:       space.Sid,
			Cpu:       space.Resourcelimit.Cpu,
			Memory:    space.Resourcelimit.Memory,
			Storage:   space.Resourcelimit.Storage,
			Hardware:  hardware,
			Image:     space.Image,
			Port:      space.Port,
			MountPath: space.VolumeMountPath,
			Operation: v1.WorkSpaceStart,
		},
	}
}

func (s *WorkSpaceService) checkWorkspaceExist(ctx context.Context, key client.ObjectKey, w *v1.WorkSpace) bool {
	if err := s.client.Get(ctx, key, w); err != nil {
		if errors.IsNotFound(err) {
			return false
		}

		klog.Errorf("get workspace error:%v", err)
		return false
	}

	return true
}

func (s *WorkSpaceService) waitForPodRunning(ctx context.Context, key client.ObjectKey, wp *v1.WorkSpace) (*pb.WorkspaceRunningInfo, error) {
	s.logger.Info("waiting for pod ready", "name:", key.Name)

	//TODO 时间的宏
	c, cancelFunc := context.WithTimeout(ctx, time.Second*120)
	defer cancelFunc()
	err := s.waitFor(c, key)
	if err != nil {
		return nil, err
	}

	info, err := s.GetPodSpaceInfo(ctx, &pb.QueryOption{
		Name:      key.Name,
		Namespace: key.Namespace,
	})

	if err == nil {
		return &pb.WorkspaceRunningInfo{
			NodeName: info.NodeName,
			Ip:       info.Ip,
			Port:     info.Port,
		}, nil
	}

	//到这儿已经出错了
	s.logger.Error(err, "wait for pod deleted")
	s.StopSpace(ctx, &pb.QueryOption{
		Name:      wp.Name,
		Namespace: wp.Namespace,
	})
	if err != nil {
		s.logger.Error(err, "stop workspace")
	}

	return EmptyWorkspaceRunningInfo, status.Error(codes.ResourceExhausted, WorkspaceStartFailed)
}

func (s *WorkSpaceService) waitFor(ctx context.Context, key client.ObjectKey) error {
	//超时
	err := errors.NewTimeoutError("get pod status error", 120)
	times := 10
	for times > 0 {

		_, internalerr := s.GetPodSpaceStatus(ctx, &pb.QueryOption{
			Name:      key.Name,
			Namespace: key.Namespace,
		})
		if internalerr == nil {
			return nil
		}
		s.logger.Info("get pod status failed:", "err:", err)
		time.Sleep(time.Second * 5)

		times--
	}

	return err
}

func getWorkspaceName(info *pb.WorkspaceInfo) string {
	return getpodname(info.Uid, info.Sid)
}

func getpodname(uid, sid string) string {
	return fmt.Sprintf("cloud-%s-%s", uid, sid)
}
