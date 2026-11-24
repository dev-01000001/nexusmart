package service

import (
	"context"
	"strings"
	"time"

	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/user/internal/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	vnDateLayout             = "2006-01-02"
	maxPlatformStatsDays     = 366
	defaultPlatformStatsDays = 30
)

// vietnamLocation is fixed UTC+07:00 (Asia/Ho_Chi_Minh has no DST).
var vietnamLocation = time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)

func parseVNDateRange(fromRaw, toRaw string, now time.Time) (fromStr, toStr string, startUTC, endUTC time.Time, dates []string, err error) {
	nowVN := now.In(vietnamLocation)
	todayVN := time.Date(nowVN.Year(), nowVN.Month(), nowVN.Day(), 0, 0, 0, 0, vietnamLocation)

	toRaw = strings.TrimSpace(toRaw)
	fromRaw = strings.TrimSpace(fromRaw)

	var toDay time.Time
	if toRaw == "" {
		toDay = todayVN
	} else {
		if len(toRaw) != len(vnDateLayout) {
			return "", "", time.Time{}, time.Time{}, nil, status.Error(codes.InvalidArgument, "to_date must be YYYY-MM-DD")
		}
		parsed, pErr := time.ParseInLocation(vnDateLayout, toRaw, vietnamLocation)
		if pErr != nil {
			return "", "", time.Time{}, time.Time{}, nil, status.Error(codes.InvalidArgument, "to_date must be YYYY-MM-DD")
		}
		toDay = parsed
	}

	var fromDay time.Time
	if fromRaw == "" {
		fromDay = toDay.AddDate(0, 0, -(defaultPlatformStatsDays - 1))
	} else {
		if len(fromRaw) != len(vnDateLayout) {
			return "", "", time.Time{}, time.Time{}, nil, status.Error(codes.InvalidArgument, "from_date must be YYYY-MM-DD")
		}
		parsed, pErr := time.ParseInLocation(vnDateLayout, fromRaw, vietnamLocation)
		if pErr != nil {
			return "", "", time.Time{}, time.Time{}, nil, status.Error(codes.InvalidArgument, "from_date must be YYYY-MM-DD")
		}
		fromDay = parsed
	}

	if fromDay.After(toDay) {
		return "", "", time.Time{}, time.Time{}, nil, status.Error(codes.InvalidArgument, "from_date must not be after to_date")
	}

	numDays := int(toDay.Sub(fromDay)/(24*time.Hour)) + 1
	if numDays > maxPlatformStatsDays {
		return "", "", time.Time{}, time.Time{}, nil, status.Error(codes.InvalidArgument, "date range must not exceed 366 days")
	}

	dates = make([]string, 0, numDays)
	for d := fromDay; !d.After(toDay); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format(vnDateLayout))
	}

	startUTC = fromDay.UTC()
	endUTC = toDay.AddDate(0, 0, 1).UTC()
	return fromDay.Format(vnDateLayout), toDay.Format(vnDateLayout), startUTC, endUTC, dates, nil
}

func (s *Server) GetPlatformUserStats(ctx context.Context, req *userv1.GetPlatformUserStatsRequest) (*userv1.GetPlatformUserStatsResponse, error) {
	if _, err := s.requireCapability(ctx, auth.CapReportsRead); err != nil {
		return nil, err
	}

	fromStr, toStr, startUTC, endUTC, dates, err := parseVNDateRange(req.GetFromDate(), req.GetToDate(), s.now())
	if err != nil {
		return nil, err
	}

	userCounts, err := s.store.GetUserPlatformCounts(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load user platform stats")
	}

	shopCounts, err := s.store.GetShopPlatformCounts(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load shop platform stats")
	}

	dailyUsersRows, err := s.store.ListDailyNewUsersVN(ctx, db.ListDailyNewUsersVNParams{
		StartTime: startUTC,
		EndTime:   endUTC,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load daily new user stats")
	}

	dailyShopsRows, err := s.store.ListDailyNewShopsVN(ctx, db.ListDailyNewShopsVNParams{
		StartTime: startUTC,
		EndTime:   endUTC,
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load daily new shop stats")
	}

	userByDay := make(map[string]int64, len(dailyUsersRows))
	for _, r := range dailyUsersRows {
		userByDay[r.VnDate] = r.Cnt
	}
	shopByDay := make(map[string]int64, len(dailyShopsRows))
	for _, r := range dailyShopsRows {
		shopByDay[r.VnDate] = r.Cnt
	}

	daily := make([]*userv1.DailyUserShopStat, 0, len(dates))
	var sumNewUsers, sumNewShops int64
	for _, d := range dates {
		nu := userByDay[d]
		ns := shopByDay[d]
		sumNewUsers += nu
		sumNewShops += ns
		daily = append(daily, &userv1.DailyUserShopStat{
			Date:     d,
			NewUsers: nu,
			NewShops: ns,
		})
	}

	return &userv1.GetPlatformUserStatsResponse{
		FromDate:     fromStr,
		ToDate:       toStr,
		TotalUsers:   userCounts.TotalUsers,
		ActiveUsers:  userCounts.ActiveUsers,
		NewUsers:     sumNewUsers,
		TotalShops:   shopCounts.TotalShops,
		ActiveShops:  shopCounts.ActiveShops,
		PendingShops: shopCounts.PendingShops,
		NewShops:     sumNewShops,
		Daily:        daily,
	}, nil
}
