package handlers

import (
	"net/http"
	"strconv"
	"time"

	"fintrack-backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type DashboardHandler struct {
	DB *gorm.DB
}

func NewDashboardHandler(db *gorm.DB) *DashboardHandler {
	return &DashboardHandler{DB: db}
}

func (h *DashboardHandler) GetDashboard(c *gin.Context) {
	userID := c.GetUint("user_id")

	// account_id query параметр байвал тухайн данс л хэргэлнэ.
	// Үгүй бол бүх данс дээгүүр нэгтгэнэ.
	accountIDStr := c.Query("account_id")
	var accountID uint
	if accountIDStr != "" {
		if v, err := strconv.ParseUint(accountIDStr, 10, 64); err == nil {
			accountID = uint(v)
		}
	}

	// Бүх данс — switcher-д үргэлж бүгдийг буцаана
	var accounts []models.Account
	h.DB.Where("user_id = ?", userID).Order("id ASC").Find(&accounts)

	var totalBalance float64
	if accountID > 0 {
		for _, a := range accounts {
			if a.ID == accountID {
				totalBalance = a.Balance
				break
			}
		}
	} else {
		for _, a := range accounts {
			totalBalance += a.Balance
		}
	}

	// Account-аар шүүх үед хуулгаас орсон хуучин огноотой гүйлгээ ч харагдах
	// шаардлагатай тул бүх хугацааны нийлбэрийг авна. Үгүй бол одоогийн сар.
	incomeQ := h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ?", userID, "income")
	expenseQ := h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ?", userID, "expense")
	recentQ := h.DB.Preload("Category").Preload("Account").
		Where("user_id = ?", userID)

	var savingsPercent, savingsAmount float64

	if accountID > 0 {
		incomeQ = incomeQ.Where("account_id = ?", accountID)
		expenseQ = expenseQ.Where("account_id = ?", accountID)
		recentQ = recentQ.Where("account_id = ?", accountID)
	} else {
		now := time.Now()
		startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		startOfLastMonth := startOfMonth.AddDate(0, -1, 0)
		incomeQ = incomeQ.Where("date >= ?", startOfMonth)
		expenseQ = expenseQ.Where("date >= ?", startOfMonth)

		// Өмнөх сартай харьцуулалт зөвхөн "бүгд" view дээр л хэрэгтэй
		var lastMonthExpenses, currentMonthExpenses float64
		h.DB.Model(&models.Transaction{}).
			Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "expense", startOfLastMonth, startOfMonth).
			Select("COALESCE(SUM(amount), 0)").Scan(&lastMonthExpenses)
		h.DB.Model(&models.Transaction{}).
			Where("user_id = ? AND type = ? AND date >= ?", userID, "expense", startOfMonth).
			Select("COALESCE(SUM(amount), 0)").Scan(&currentMonthExpenses)
		if lastMonthExpenses > 0 {
			savingsAmount = lastMonthExpenses - currentMonthExpenses
			savingsPercent = (savingsAmount / lastMonthExpenses) * 100
		}
	}

	var totalIncome, totalExpenses float64
	incomeQ.Select("COALESCE(SUM(amount), 0)").Scan(&totalIncome)
	expenseQ.Select("COALESCE(SUM(amount), 0)").Scan(&totalExpenses)

	var recentTx []models.Transaction
	recentQ.Order("date DESC, created_at DESC").Limit(10).Find(&recentTx)

	c.JSON(http.StatusOK, models.DashboardResponse{
		Balance:            totalBalance,
		TotalIncome:        totalIncome,
		TotalExpenses:      totalExpenses,
		SavingsPercent:     savingsPercent,
		SavingsAmount:      savingsAmount,
		Accounts:           accounts,
		RecentTransactions: recentTx,
	})
}

func (h *DashboardHandler) GetExpensesSummary(c *gin.Context) {
	userID := c.GetUint("user_id")
	period := c.DefaultQuery("period", "monthly")
	accountIDStr := c.Query("account_id")
	var accountID uint
	if accountIDStr != "" {
		if v, err := strconv.ParseUint(accountIDStr, 10, 64); err == nil {
			accountID = uint(v)
		}
	}

	type categoryResult struct {
		CategoryID uint
		Name       string
		Color      string
		Icon       string
		Total      float64
	}

	q := h.DB.Model(&models.Transaction{}).
		Select("transactions.category_id, categories.name, categories.color, categories.icon, SUM(transactions.amount) as total").
		Joins("JOIN categories ON categories.id = transactions.category_id").
		Where("transactions.user_id = ? AND transactions.type = ?", userID, "expense")

	// Account-аар шүүх үед хугацааны хязгаарлалт хийхгүй (хуулгын хуучин огноо
	// бүгд харагдах ёстой). Үгүй үед сонгосон period-ээр хязгаарлана.
	if accountID > 0 {
		q = q.Where("transactions.account_id = ?", accountID)
	} else {
		now := time.Now()
		var startDate time.Time
		switch period {
		case "daily":
			startDate = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		case "weekly":
			startDate = now.AddDate(0, 0, -7)
		case "monthly":
			startDate = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		case "yearly":
			startDate = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
		}
		q = q.Where("transactions.date >= ?", startDate)
	}

	var results []categoryResult
	q.Group("transactions.category_id, categories.name, categories.color, categories.icon").
		Order("total DESC").
		Scan(&results)

	var total float64
	for _, r := range results {
		total += r.Total
	}

	categories := make([]models.CategoryExpense, len(results))
	for i, r := range results {
		pct := 0.0
		if total > 0 {
			pct = (r.Total / total) * 100
		}
		categories[i] = models.CategoryExpense{
			CategoryID:   r.CategoryID,
			CategoryName: r.Name,
			Color:        r.Color,
			Icon:         r.Icon,
			Amount:       r.Total,
			Percentage:   pct,
		}
	}

	c.JSON(http.StatusOK, models.ExpensesSummary{
		Total:      total,
		Categories: categories,
	})
}

func (h *DashboardHandler) GetStatistics(c *gin.Context) {
	userID := c.GetUint("user_id")
	period := c.DefaultQuery("period", "monthly")
	accountIDStr := c.Query("account_id")
	var accountID uint
	if accountIDStr != "" {
		if v, err := strconv.ParseUint(accountIDStr, 10, 64); err == nil {
			accountID = uint(v)
		}
	}

	now := time.Now()
	var periods []models.PeriodStats

	sumBetween := func(txType string, start, end time.Time) float64 {
		q := h.DB.Model(&models.Transaction{}).
			Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, txType, start, end)
		if accountID > 0 {
			q = q.Where("account_id = ?", accountID)
		}
		var v float64
		q.Select("COALESCE(SUM(amount), 0)").Scan(&v)
		return v
	}

	switch period {
	case "daily":
		for i := 6; i >= 0; i-- {
			day := now.AddDate(0, 0, -i)
			start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
			end := start.AddDate(0, 0, 1)
			periods = append(periods, models.PeriodStats{
				Label:    start.Format("Mon"),
				Income:   sumBetween("income", start, end),
				Expenses: sumBetween("expense", start, end),
			})
		}
	case "weekly":
		for i := 5; i >= 0; i-- {
			end := now.AddDate(0, 0, -i*7)
			start := end.AddDate(0, 0, -7)
			periods = append(periods, models.PeriodStats{
				Label:    start.Format("Jan 2"),
				Income:   sumBetween("income", start, end),
				Expenses: sumBetween("expense", start, end),
			})
		}
	case "monthly":
		for i := 5; i >= 0; i-- {
			month := now.AddDate(0, -i, 0)
			start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())
			end := start.AddDate(0, 1, 0)
			periods = append(periods, models.PeriodStats{
				Label:    start.Format("Jan"),
				Income:   sumBetween("income", start, end),
				Expenses: sumBetween("expense", start, end),
			})
		}
	case "yearly":
		for i := 3; i >= 0; i-- {
			year := now.Year() - i
			start := time.Date(year, 1, 1, 0, 0, 0, 0, now.Location())
			end := start.AddDate(1, 0, 0)
			periods = append(periods, models.PeriodStats{
				Label:    strconv.Itoa(year),
				Income:   sumBetween("income", start, end),
				Expenses: sumBetween("expense", start, end),
			})
		}
	}

	// Хэрэв account_id өгөгдсөн бол period-ээс үл хамаарч бүх хугацааны нийлбэрийг
	// үндсэн карта дээр харуулна. Үгүй бол period-ийн нийлбэр.
	var totalIncome, totalExpenses float64
	if accountID > 0 {
		h.DB.Model(&models.Transaction{}).
			Where("user_id = ? AND type = ? AND account_id = ?", userID, "income", accountID).
			Select("COALESCE(SUM(amount), 0)").Scan(&totalIncome)
		h.DB.Model(&models.Transaction{}).
			Where("user_id = ? AND type = ? AND account_id = ?", userID, "expense", accountID).
			Select("COALESCE(SUM(amount), 0)").Scan(&totalExpenses)
	} else {
		var periodStart time.Time
		switch period {
		case "daily":
			periodStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		case "weekly":
			periodStart = now.AddDate(0, 0, -7)
		case "monthly":
			periodStart = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		case "yearly":
			periodStart = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
		}
		h.DB.Model(&models.Transaction{}).
			Where("user_id = ? AND type = ? AND date >= ?", userID, "income", periodStart).
			Select("COALESCE(SUM(amount), 0)").Scan(&totalIncome)
		h.DB.Model(&models.Transaction{}).
			Where("user_id = ? AND type = ? AND date >= ?", userID, "expense", periodStart).
			Select("COALESCE(SUM(amount), 0)").Scan(&totalExpenses)
	}

	c.JSON(http.StatusOK, models.StatisticsResponse{
		Income:   totalIncome,
		Expenses: totalExpenses,
		Periods:  periods,
	})
}
