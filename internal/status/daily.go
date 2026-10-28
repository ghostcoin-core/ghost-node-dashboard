package status

import (
	"math/big"
	"time"
)

// DailyRewards is the blocks won and their reward per local day, oldest
// first and ending today. Days without a win are listed with zeros.
type DailyRewards struct {
	// Days7 and Days30 are the last 7 and 30 days.
	Days7  []DayRewards `json:"days7"`
	Days30 []DayRewards `json:"days30"`
	// All runs from the day of the first win to today; empty without wins.
	All []DayRewards `json:"all"`
}

// DayRewards is one local day. A block won whose reward is unknown counts in
// Won and adds nothing to Reward.
type DayRewards struct {
	Day    string `json:"day"`
	Won    int    `json:"won"`
	Reward Amount `json:"reward"`
}

// PeriodTotal is the blocks won and their reward over some days.
type PeriodTotal struct {
	Won    int    `json:"won"`
	Reward Amount `json:"reward"`
}

const dayLayout = "2006-01-02"

type daySum struct {
	won    int
	reward *big.Int
}

// daily works out the per-day lists and the last-7-days total from the
// blocks won, in the tracker's location.
func (r *Rewards) daily(won []wonBlock) (DailyRewards, PeriodTotal) {
	sums := map[string]*daySum{}
	var first time.Time
	for _, b := range won {
		t := time.Unix(int64(b.Time), 0).In(r.location)
		if first.IsZero() || t.Before(first) {
			first = t
		}
		key := t.Format(dayLayout)
		s := sums[key]
		if s == nil {
			s = &daySum{reward: new(big.Int)}
			sums[key] = s
		}
		s.won++
		if reward, ok := new(big.Int).SetString(b.Reward, 10); ok {
			s.reward.Add(s.reward, reward)
		}
	}

	y, m, d := r.now().In(r.location).Date()
	// Noon keeps a day a day when the clocks change.
	day := func(back int) time.Time { return time.Date(y, m, d-back, 12, 0, 0, 0, r.location) }
	row := func(t time.Time) DayRewards {
		key := t.Format(dayLayout)
		if s := sums[key]; s != nil {
			return DayRewards{Day: key, Won: s.won, Reward: amount(s.reward)}
		}
		return DayRewards{Day: key, Reward: amount(new(big.Int))}
	}
	days := func(n int) []DayRewards {
		out := make([]DayRewards, 0, n)
		for back := n - 1; back >= 0; back-- {
			out = append(out, row(day(back)))
		}
		return out
	}

	out := DailyRewards{Days7: days(7), Days30: days(30), All: []DayRewards{}}
	if !first.IsZero() {
		fy, fm, fd := first.Date()
		start := time.Date(fy, fm, fd, 12, 0, 0, 0, r.location)
		n := 0
		for !day(n).Before(start) {
			n++
		}
		out.All = days(n)
	}

	last7 := PeriodTotal{}
	sum := new(big.Int)
	for _, dr := range out.Days7 {
		last7.Won += dr.Won
		if s := sums[dr.Day]; s != nil {
			sum.Add(sum, s.reward)
		}
	}
	last7.Reward = amount(sum)
	return out, last7
}
