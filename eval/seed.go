//go:build eval

package eval

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Frozelo/thingsctl/internal/thingsdb"
)

// Seed ids. Titles below are what task prompts and graders refer to.
const (
	AreaHome   = "AhomeAreaXXXXXXXXXXXXX"
	AreaWork   = "AworkAreaXXXXXXXXXXXXX"
	AreaHealth = "AhealthAreaXXXXXXXXXXX"

	TagHome    = "GhomeTagXXXXXXXXXXXXXX"
	TagWork    = "GworkTagXXXXXXXXXXXXXX"
	TagUrgent  = "GurgentTagXXXXXXXXXXXX"
	TagErrand  = "GerrandTagXXXXXXXXXXXX"
	TagWaiting = "GwaitingTagXXXXXXXXXXX"

	ProjWebsite = "PwebsiteXXXXXXXXXXXXXX"
	ProjBudget  = "PbudgetXXXXXXXXXXXXXXX"
	ProjKitchen = "PkitchenXXXXXXXXXXXXXX"
	ProjTaxes   = "PtaxesXXXXXXXXXXXXXXXX"
	ProjSpanish = "PspanishXXXXXXXXXXXXXX"
	HeadDesign  = "HdesignXXXXXXXXXXXXXXX"
	HeadLaunch  = "HlaunchXXXXXXXXXXXXXXX"

	TPlumber    = "TplumberXXXXXXXXXXXXXX"
	TFlights    = "TflightsXXXXXXXXXXXXXX"
	TArticle    = "TarticleXXXXXXXXXXXXXX"
	TNotes      = "TrelnotesXXXXXXXXXXXXX"
	TElectric   = "TelectricXXXXXXXXXXXXX"
	TGym        = "TgymXXXXXXXXXXXXXXXXXX"
	TReviewPR   = "TreviewprXXXXXXXXXXXXX"
	TDentist    = "TdentistXXXXXXXXXXXXXX"
	TBirthday   = "TbirthdayXXXXXXXXXXXXX"
	TMonitor    = "TmonitorXXXXXXXXXXXXXX"
	TBatteries  = "TbatteriesXXXXXXXXXXXX"
	TPaint      = "TpaintXXXXXXXXXXXXXXXX"
	TTap        = "TtapXXXXXXXXXXXXXXXXXX"
	THomepage   = "ThomepageXXXXXXXXXXXXX"
	TReceipts   = "TreceiptsXXXXXXXXXXXXX"
	TForms      = "TformsXXXXXXXXXXXXXXXX"
	TJuggle     = "TjuggleXXXXXXXXXXXXXXX"
	TIceland    = "TicelandXXXXXXXXXXXXXX"
	TDuolingo   = "TduolingoXXXXXXXXXXXXX"
	TPassport   = "TpassportXXXXXXXXXXXXX"
	TSpreadshet = "TspreadshXXXXXXXXXXXXX"
	TCabinets   = "TcabinetsXXXXXXXXXXXXX"
)

// BuildSeed writes the eval database to path. today anchors relative dates.
func BuildSeed(path, schemaPath string, today thingsdb.Date) error {
	os.Remove(path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return err
	}
	var firstErr error
	exec := func(q string, args ...any) {
		if firstErr != nil {
			return
		}
		if _, err := db.Exec(q, args...); err != nil {
			firstErr = fmt.Errorf("%s: %w", q, err)
		}
	}
	exec(string(schema))
	exec(`INSERT INTO Meta(key,value) VALUES('databaseVersion','<?xml version="1.0"?><plist version="1.0"><integer>29</integer></plist>')`)
	exec(`INSERT INTO TMSettings(uuid, uriSchemeAuthenticationToken) VALUES('settings','evaltoken')`)
	exec(`INSERT INTO TMArea(uuid,title,visible,"index") VALUES(?,?,1,1),(?,?,1,2),(?,?,1,3)`,
		AreaHome, "Home", AreaWork, "Work", AreaHealth, "Health")
	exec(`INSERT INTO TMTag(uuid,title,parent,"index") VALUES(?,?,NULL,1),(?,?,NULL,2),(?,?,NULL,3),(?,?,NULL,4),(?,?,NULL,5)`,
		TagHome, "home", TagWork, "work", TagUrgent, "urgent", TagErrand, "errand", TagWaiting, "waiting")

	day := func(n int) int64 { return int64(today.AddDays(n)) }
	now := float64(time.Now().Unix())
	idx := 0
	type row map[string]any
	task := func(uuid string, r row) {
		idx++
		cols := []string{"uuid", "type", "status", "trashed", "start", "creationDate", "userModificationDate", "index"}
		vals := []any{uuid, 0, 0, 0, 1, now - 86400*7, now - 3600, idx}
		for k, v := range r {
			replaced := false
			for i, c := range cols {
				if c == k {
					vals[i], replaced = v, true
				}
			}
			if !replaced {
				cols = append(cols, k)
				vals = append(vals, v)
			}
		}
		q := make([]string, len(cols))
		for i, c := range cols {
			q[i] = `"` + c + `"`
		}
		exec(fmt.Sprintf(`INSERT INTO TMTask(%s) VALUES(%s)`, strings.Join(q, ","),
			strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")), vals...)
	}
	evening := int64(thingsdb.NewClock(18, 0))

	task(ProjWebsite, row{"type": 1, "title": "Website redesign", "area": AreaWork, "openUntrashedLeafActionsCount": 3})
	task(ProjBudget, row{"type": 1, "title": "Q4 budget", "area": AreaWork, "deadline": day(10), "openUntrashedLeafActionsCount": 1})
	task(ProjKitchen, row{"type": 1, "title": "Kitchen renovation", "area": AreaHome, "openUntrashedLeafActionsCount": 3})
	task(ProjTaxes, row{"type": 1, "title": "Taxes 2026", "area": AreaHome, "openUntrashedLeafActionsCount": 2})
	task(ProjSpanish, row{"type": 1, "title": "Learn Spanish", "start": 2, "openUntrashedLeafActionsCount": 1})
	task(HeadDesign, row{"type": 2, "title": "Design", "project": ProjWebsite})
	task(HeadLaunch, row{"type": 2, "title": "Launch", "project": ProjWebsite})

	task(TPlumber, row{"title": "Call plumber", "start": 0})
	task(TFlights, row{"title": "Book flights to Lisbon", "start": 0})
	task(TArticle, row{"title": "Read article on Go generics", "start": 0, "notes": "https://go.dev/blog/intro-generics"})
	task(TNotes, row{"title": "Write release notes", "heading": HeadLaunch, "startDate": day(0), "todayIndex": 1})
	task(TElectric, row{"title": "Pay electricity bill", "area": AreaHome, "startDate": day(0), "deadline": day(0), "todayIndex": 2})
	task(TReviewPR, row{"title": "Review PR #42", "area": AreaWork, "startDate": day(0), "todayIndex": 3})
	task(TGym, row{"title": "Gym", "area": AreaHealth, "startDate": day(0), "startBucket": 1, "reminderTime": evening, "todayIndex": 4})
	task(TDentist, row{"title": "Dentist appointment", "area": AreaHealth, "start": 2, "startDate": day(3), "reminderTime": int64(thingsdb.NewClock(9, 30))})
	task(TBirthday, row{"title": "Mom's birthday", "start": 2, "startDate": day(7)})
	task(TMonitor, row{"title": "Order new monitor", "area": AreaWork})
	task(TBatteries, row{"title": "Buy batteries", "area": AreaHome})
	task(TPaint, row{"title": "Pick paint colors", "project": ProjKitchen})
	task(TTap, row{"title": "Fix leaking tap", "project": ProjKitchen, "notes": "Washer size: 1/2 inch"})
	task(TCabinets, row{"title": "Measure cabinets", "project": ProjKitchen, "checklistItemsCount": 2, "openChecklistItemsCount": 2})
	task(THomepage, row{"title": "Draft homepage copy", "heading": HeadDesign})
	task(TSpreadshet, row{"title": "Update budget spreadsheet", "project": ProjBudget})
	task(TReceipts, row{"title": "Collect receipts", "project": ProjTaxes})
	task(TForms, row{"title": "Download tax forms", "project": ProjTaxes})
	task(TJuggle, row{"title": "Learn to juggle", "start": 2})
	task(TIceland, row{"title": "Visit Iceland", "start": 2})
	task(TDuolingo, row{"title": "Duolingo lesson", "project": ProjSpanish})
	task(TPassport, row{"title": "Renew passport", "status": 3, "stopDate": now - 86400})

	exec(`INSERT INTO TMTaskTag(tasks,tags) VALUES(?,?),(?,?),(?,?),(?,?),(?,?),(?,?),(?,?),(?,?),(?,?)`,
		TNotes, TagWork, TElectric, TagHome, TReviewPR, TagUrgent, TReviewPR, TagWork,
		TMonitor, TagErrand, TBatteries, TagErrand, TReceipts, TagHome, TSpreadshet, TagUrgent, TTap, TagHome)
	exec(`INSERT INTO TMChecklistItem(uuid,title,status,"index",task) VALUES('CwidthXXXXXXXXXXXXXXXX','width',0,1,?),('CheightXXXXXXXXXXXXXXX','height',0,2,?)`,
		TCabinets, TCabinets)
	return firstErr
}
