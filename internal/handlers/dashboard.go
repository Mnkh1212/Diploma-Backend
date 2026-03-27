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
	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	startOfLastMonth := startOfMonth.AddDate(0, -1, 0)

	// Total balance across all accounts
	var accounts []models.Account
	h.DB.Where("user_id = ?", userID).Find(&accounts)

	var totalBalance float64
	for _, a := range accounts {
		totalBalance += a.Balance
	}

	// Current month income/expenses
	var totalIncome, totalExpenses float64
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ?", userID, "income", startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&totalIncome)

	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ?", userID, "expense", startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&totalExpenses)

	// Last month expenses for comparison
	var lastMonthExpenses float64
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "expense", startOfLastMonth, startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&lastMonthExpenses)

	var savingsPercent, savingsAmount float64
	if lastMonthExpenses > 0 {
		savingsAmount = lastMonthExpenses - totalExpenses
		savingsPercent = (savingsAmount / lastMonthExpenses) * 100
	}

	// Recent transactions
	var recentTx []models.Transaction
	h.DB.Preload("Category").Preload("Account").
		Where("user_id = ?", userID).
		Order("date DESC, created_at DESC").
		Limit(10).
		Find(&recentTx)

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

	type categoryResult struct {
		CategoryID uint
		Name       string
		Color      string
		Icon       string
		Total      float64
	}

	var results []categoryResult
	h.DB.Model(&models.Transaction{}).
		Select("transactions.category_id, categories.name, categories.color, categories.icon, SUM(transactions.amount) as total").
		Joins("JOIN categories ON categories.id = transactions.category_id").
		Where("transactions.user_id = ? AND transactions.type = ? AND transactions.date >= ?", userID, "expense", startDate).
		Group("transactions.category_id, categories.name, categories.color, categories.icon").
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

	now := time.Now()
	var periods []models.PeriodStats

	switch period {
	case "daily":
		for i := 6; i >= 0; i-- {
			day := now.AddDate(0, 0, -i)
			start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
			end := start.AddDate(0, 0, 1)

			var income, expenses float64
			h.DB.Model(&models.Transaction{}).
				Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "income", start, end).
				Select("COALESCE(SUM(amount), 0)").Scan(&income)
			h.DB.Model(&models.Transaction{}).
				Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "expense", start, end).
				Select("COALESCE(SUM(amount), 0)").Scan(&expenses)

			periods = append(periods, models.PeriodStats{
				Label:    start.Format("Mon"),
				Income:   income,
				Expenses: expenses,
			})
		}
	case "weekly":
		for i := 5; i >= 0; i-- {
			end := now.AddDate(0, 0, -i*7)
			start := end.AddDate(0, 0, -7)

			var income, expenses float64
			h.DB.Model(&models.Transaction{}).
				Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "income", start, end).
				Select("COALESCE(SUM(amount), 0)").Scan(&income)
			h.DB.Model(&models.Transaction{}).
				Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "expense", start, end).
				Select("COALESCE(SUM(amount), 0)").Scan(&expenses)

			periods = append(periods, models.PeriodStats{
				Label:    start.Format("Jan 2"),
				Income:   income,
				Expenses: expenses,
			})
		}
	case "monthly":
		for i := 5; i >= 0; i-- {
			month := now.AddDate(0, -i, 0)
			start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())
			end := start.AddDate(0, 1, 0)

			var income, expenses float64
			h.DB.Model(&models.Transaction{}).
				Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "income", start, end).
				Select("COALESCE(SUM(amount), 0)").Scan(&income)
			h.DB.Model(&models.Transaction{}).
				Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "expense", start, end).
				Select("COALESCE(SUM(amount), 0)").Scan(&expenses)

			periods = append(periods, models.PeriodStats{
				Label:    start.Format("Jan"),
				Income:   income,
				Expenses: expenses,
			})
		}
	case "yearly":
		for i := 3; i >= 0; i-- {
			year := now.Year() - i
			start := time.Date(year, 1, 1, 0, 0, 0, 0, now.Location())
			end := start.AddDate(1, 0, 0)

			var income, expenses float64
			h.DB.Model(&models.Transaction{}).
				Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "income", start, end).
				Select("COALESCE(SUM(amount), 0)").Scan(&income)
			h.DB.Model(&models.Transaction{}).
				Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "expense", start, end).
				Select("COALESCE(SUM(amount), 0)").Scan(&expenses)

			periods = append(periods, models.PeriodStats{
				Label:    strconv.Itoa(year),
				Income:   income,
				Expenses: expenses,
			})
		}
	}

	// Current month totals
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	var totalIncome, totalExpenses float64
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ?", userID, "income", startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&totalIncome)
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ?", userID, "expense", startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&totalExpenses)

	c.JSON(http.StatusOK, models.StatisticsResponse{
		Income:   totalIncome,
		Expenses: totalExpenses,
		Periods:  periods,
	})
}
