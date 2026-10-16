package basemap

import "context"

func (s *Store) Dashboard(ctx context.Context, info SystemInfo, flash string, createdToken string) (DashboardData, error) {
	basemaps, err := s.ListBasemaps(ctx)
	if err != nil {
		return DashboardData{}, err
	}
	vectorAssets, err := s.ListVectorAssets(ctx)
	if err != nil {
		return DashboardData{}, err
	}
	scanSources, err := s.ListScanSources(ctx)
	if err != nil {
		return DashboardData{}, err
	}
	scanRuns, err := s.ListScanRuns(ctx, 10)
	if err != nil {
		return DashboardData{}, err
	}
	targetSystems, err := s.ListTargetSystems(ctx)
	if err != nil {
		return DashboardData{}, err
	}
	tokens, err := s.ListTokens(ctx)
	if err != nil {
		return DashboardData{}, err
	}
	pushRecords, err := s.ListPushRecords(ctx, 10)
	if err != nil {
		return DashboardData{}, err
	}
	vectorPushRecords, err := s.ListVectorPushRecords(ctx, 10)
	if err != nil {
		return DashboardData{}, err
	}
	versionHealth, err := s.loadVersionHealth(ctx)
	if err != nil {
		return DashboardData{}, err
	}
	vectorHealth, err := s.loadVectorHealth(ctx)
	if err != nil {
		return DashboardData{}, err
	}
	tileJobs, err := s.ListTileJobs(ctx, 10)
	if err != nil {
		return DashboardData{}, err
	}
	layers, err := s.ListLayerInventories(ctx)
	if err != nil {
		return DashboardData{}, err
	}
	return DashboardData{
		SystemInfo:        info,
		Basemaps:          basemaps,
		VectorAssets:      vectorAssets,
		ScanSources:       scanSources,
		ScanRuns:          scanRuns,
		TargetSystems:     targetSystems,
		Tokens:            tokens,
		PushRecords:       pushRecords,
		VectorPushRecords: vectorPushRecords,
		Flash:             flash,
		CreatedToken:      createdToken,
		VersionHealth:     versionHealth,
		VectorHealth:      vectorHealth,
		TileJobs:          tileJobs,
		Layers:            layers,
	}, nil
}
