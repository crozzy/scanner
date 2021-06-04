package imagescan

import (
	"context"

	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/sirupsen/logrus"
	"github.com/stackrox/rox/pkg/stringutils"
	apiGRPC "github.com/stackrox/scanner/api/grpc"
	apiV1 "github.com/stackrox/scanner/api/v1"
	"github.com/stackrox/scanner/cpe/nvdtoolscache"
	"github.com/stackrox/scanner/database"
	v1 "github.com/stackrox/scanner/generated/shared/api/v1"
	"github.com/stackrox/scanner/pkg/clairify/types"
	"github.com/stackrox/scanner/pkg/commonerr"
	server "github.com/stackrox/scanner/pkg/scan"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Service defines the image scanning service.
type Service interface {
	apiGRPC.APIService

	v1.ImageScanServiceServer
}

// NewService returns the service for image scanning
func NewService(db database.Datastore, nvdCache nvdtoolscache.Cache) Service {
	return &serviceImpl{
		db:       db,
		nvdCache: nvdCache,
	}
}

type serviceImpl struct {
	db       database.Datastore
	nvdCache nvdtoolscache.Cache
}

func (s *serviceImpl) GetLanguageLevelComponents(ctx context.Context, req *v1.GetLanguageLevelComponentsRequest) (*v1.GetLanguageLevelComponentsResponse, error) {
	layerName, lineage, err := s.getLayerNameFromImageReq(req)
	if err != nil {
		return nil, err
	}
	components, err := s.db.GetLayerLanguageComponents(layerName, lineage, &database.DatastoreOptions{
		UncertifiedRHEL: req.GetUncertifiedRHEL(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to retrieve components from DB: %v", err)
	}
	return &v1.GetLanguageLevelComponentsResponse{
		LayerToComponents: convertComponents(components),
	}, nil
}

func (s *serviceImpl) ScanImage(ctx context.Context, req *v1.ScanImageRequest) (*v1.ScanImageResponse, error) {
	image, err := types.GenerateImageFromString(req.GetImage())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "could not parse image %q", req.GetImage())
	}

	reg := req.GetRegistry()

	digest, err := server.ProcessImage(s.db, image, reg.GetUrl(), reg.GetUsername(), reg.GetPassword(), reg.GetInsecure(), req.GetUncertifiedRHEL())
	if err != nil {
		return nil, err
	}

	return &v1.ScanImageResponse{
		Status: v1.ScanStatus_SUCCEEDED,
		Image: &v1.ImageSpec{
			Digest: digest,
			Image:  image.TaggedName(),
		},
	}, nil
}

func (s *serviceImpl) getLayer(layerName, lineage string, uncertifiedRHEL bool) (*v1.GetImageScanResponse, error) {
	opts := &database.DatastoreOptions{
		WithFeatures:        true,
		WithVulnerabilities: true,
		UncertifiedRHEL:     uncertifiedRHEL,
	}

	dbLayer, err := s.db.FindLayer(layerName, lineage, opts)
	if err == commonerr.ErrNotFound {
		return nil, status.Errorf(codes.NotFound, "Could not find Clair layer %q", layerName)
	} else if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// This endpoint is not used, so not going to bother with notes until they are necessary.
	layer, _, err := apiV1.LayerFromDatabaseModel(s.db, dbLayer, lineage, opts)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	features, err := convertFeatures(layer.Features)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "error converting features: %v", err)
	}

	return &v1.GetImageScanResponse{
		Status: v1.ScanStatus_SUCCEEDED,
		Image: &v1.Image{
			Features: features,
		},
	}, nil
}

type imageRequest interface {
	GetImageSpec() *v1.ImageSpec
	GetUncertifiedRHEL() bool
}

func (s *serviceImpl) getLayerNameFromImageReq(req imageRequest) (string, string, error) {
	imgSpec := req.GetImageSpec()

	if stringutils.AllEmpty(imgSpec.GetImage(), imgSpec.GetDigest()) {
		return "", "", status.Error(codes.InvalidArgument, "either image or digest must be specified")
	}

	var layerFetcher func(s string, opts *database.DatastoreOptions) (string, string, bool, error)
	var argument string
	if digest := imgSpec.GetDigest(); digest != "" {
		logrus.Debugf("Getting layer SHA by digest %s", digest)
		argument = digest
		layerFetcher = s.db.GetLayerBySHA
	} else {
		logrus.Debugf("Getting layer SHA by image %s", imgSpec.GetImage())
		argument = imgSpec.GetImage()
		layerFetcher = s.db.GetLayerByName
	}
	layerName, lineage, exists, err := layerFetcher(argument, &database.DatastoreOptions{
		UncertifiedRHEL: req.GetUncertifiedRHEL(),
	})
	if err != nil {
		return "", "", err
	}
	if !exists {
		return "", "", status.Errorf(codes.NotFound, "image with reference %q not found", argument)
	}
	return layerName, lineage, nil
}

func (s *serviceImpl) GetImageScan(ctx context.Context, req *v1.GetImageScanRequest) (*v1.GetImageScanResponse, error) {
	layerName, lineage, err := s.getLayerNameFromImageReq(req)
	if err != nil {
		return nil, err
	}
	return s.getLayer(layerName, lineage, req.GetUncertifiedRHEL())
}

// RegisterServiceServer registers this service with the given gRPC Server.
func (s *serviceImpl) RegisterServiceServer(grpcServer *grpc.Server) {
	v1.RegisterImageScanServiceServer(grpcServer, s)
}

// RegisterServiceHandler registers this service with the given gRPC Gateway endpoint.
func (s *serviceImpl) RegisterServiceHandler(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error {
	return v1.RegisterImageScanServiceHandler(ctx, mux, conn)
}
