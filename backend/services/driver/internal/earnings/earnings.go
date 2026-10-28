// Package earnings turns finished routes into the driver's trip history and earnings.
package earnings

import (
	"context"
	"math"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/services/driver/internal/tasks"
)

// The driver's share of a fare, and the unit pay is rounded to.
const (
	driverShare = 0.7
	payUnitRp   = 500
)

// RouteRp is the driver's pay for a route that cost costRp: 70% of the fare rounded to Rp 500. It is
// frontend/packages/domain/src/earnings.ts to the letter, floating point included (22,500 pays 15,500 there,
// because 22500*0.7/500 comes out just under 31.5), so the estimate on a task card and the history never differ.
func RouteRp(costRp int64) int64 {
	return int64(math.Round(float64(costRp)*driverShare/payUnitRp)) * payUnitRp
}

// HistoryItem is the frontend's DriverHistoryItem.
type HistoryItem struct {
	PlanID      string         `json:"planId"`
	PlanCode    string         `json:"planCode"`
	RouteID     string         `json:"routeId"`
	FinishedAt  contracts.Time `json:"finishedAt"`
	Stops       int            `json:"stops"`
	DistanceKm  float64        `json:"distanceKm"`
	DurationMin float64        `json:"durationMin"`
	EarningsRp  int64          `json:"earningsRp"`
}

// Day is one entry of Summary.ByDay.
type Day struct {
	Date     string `json:"date"`
	AmountRp int64  `json:"amountRp"`
	Trips    int    `json:"trips"`
}

// Summary is the frontend's DriverEarnings: today and the last seven days, days counted in WIB.
type Summary struct {
	TodayRp    int64 `json:"todayRp"`
	WeekRp     int64 `json:"weekRp"`
	TripsToday int   `json:"tripsToday"`
	TripsWeek  int   `json:"tripsWeek"`
	ByDay      []Day `json:"byDay"`
}

// wib is the time zone the app's days follow (UTC+7, no daylight saving).
var wib = time.FixedZone("WIB", 7*60*60)

const (
	dateLayout   = "2006-01-02"
	historyLimit = 200
)

func itemOf(f tasks.Finished) HistoryItem {
	return HistoryItem{
		PlanID: f.PlanID, PlanCode: f.PlanCode, RouteID: f.RouteID, FinishedAt: contracts.At(f.FinishedAt),
		Stops: f.Stops, DistanceKm: f.DistanceKm, DurationMin: f.DurationMin, EarningsRp: RouteRp(f.CostRp),
	}
}

// summarize adds up the routes finished in the seven WIB days ending with the day of now; older ones are ignored.
func summarize(finished []tasks.Finished, now time.Time) Summary {
	today := now.In(wib)
	byDay := make([]Day, 7)
	index := map[string]int{}
	for i := range byDay {
		date := today.AddDate(0, 0, i-6).Format(dateLayout)
		byDay[i], index[date] = Day{Date: date}, i
	}
	for _, f := range finished {
		if i, ok := index[f.FinishedAt.In(wib).Format(dateLayout)]; ok {
			byDay[i].AmountRp += RouteRp(f.CostRp)
			byDay[i].Trips++
		}
	}
	s := Summary{ByDay: byDay, TodayRp: byDay[6].AmountRp, TripsToday: byDay[6].Trips}
	for _, d := range byDay {
		s.WeekRp += d.AmountRp
		s.TripsWeek += d.Trips
	}
	return s
}

// Service reads the history and earnings of a driver.
type Service struct {
	db  tasks.DB
	now func() time.Time
}

func NewService(db tasks.DB, now func() time.Time) *Service { return &Service{db: db, now: now} }

// History is driver.history: the most recent finished routes, newest first.
func (s *Service) History(ctx context.Context, driverID string) ([]HistoryItem, error) {
	finished, err := tasks.FinishedSince(ctx, s.db, driverID, time.Unix(0, 0), historyLimit)
	if err != nil {
		return nil, err
	}
	items := make([]HistoryItem, len(finished))
	for i, f := range finished {
		items[i] = itemOf(f)
	}
	return items, nil
}

// Earnings is driver.earnings.
func (s *Service) Earnings(ctx context.Context, driverID string) (Summary, error) {
	now := s.now()
	day := now.In(wib)
	weekStart := time.Date(day.Year(), day.Month(), day.Day()-6, 0, 0, 0, 0, wib)
	finished, err := tasks.FinishedSince(ctx, s.db, driverID, weekStart, 0)
	if err != nil {
		return Summary{}, err
	}
	return summarize(finished, now), nil
}
