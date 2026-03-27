# FinTrack — Ажлын бүртгэл (Work Log)

## Төслийн тодорхойлолт
**Сэдэв:** Хиймэл оюун ухаанд суурилсан хувийн санхүүгийн хяналт ба зөвлөмжийн систем
**Төрөл:** Их сургуулийн diploma ажил
**Repo:** Backend: Mnkh1212/Diploma-Backend | Frontend: Mnkh1212/Diploma-Frontend

---

## 2026-03-27 — Төслийн анхны тохиргоо (Day 1)

### Хийгдсэн ажлууд:

#### 1. Төслийн бүтэц үүсгэсэн
- `backend/` — Go backend project
- `frontend/` — React Native (Expo) project
- `docs/` — Баримт бичиг
- `docker-compose.yml` — Docker orchestration

#### 2. Backend (Go) бүтээсэн
- **Framework:** Gin (HTTP router) + GORM (ORM)
- **Authentication:** JWT token-based auth (bcrypt password hashing, 72h expiry)
- **Database:** PostgreSQL 16 (auto-migration via GORM)
- **API Version:** v1 (/api/v1/...)

**Үүсгэсэн файлууд:**
- `cmd/server/main.go` — Entry point
- `internal/config/config.go` — Environment config loader (env vars)
- `internal/database/database.go` — DB connection, migration, 17 category seed
- `internal/models/models.go` — 8 data models, 8 DTOs
- `internal/middleware/auth.go` — JWT auth middleware, CORS
- `internal/handlers/auth.go` — Register, Login, GetProfile, UpdateProfile
- `internal/handlers/transaction.go` — CRUD transactions (auto balance update)
- `internal/handlers/dashboard.go` — Dashboard, Expenses summary, Statistics
- `internal/handlers/budget.go` — CRUD budgets (auto spent calculation)
- `internal/handlers/ai_chat.go` — AI financial advisor (context-aware)
- `internal/handlers/account.go` — Accounts CRUD, Categories list
- `internal/handlers/scheduled_payment.go` — Scheduled payments CRUD + toggle
- `internal/routes/routes.go` — 32 API routes (2 public + 30 protected)

**Database Models (8 table):**
- `User` — Хэрэглэгч (name, email, password, avatar, currency)
- `Account` — Данс (bank, cash, credit_card, investment) + balance tracking
- `Category` — Ангилал (17 seed: 12 expense + 5 income)
- `Transaction` — Орлого/Зарлага (auto updates account balance)
- `Budget` — Сарын төсөв (category-based, auto spent calc)
- `ScheduledPayment` — Давтагдах төлбөр (daily/weekly/monthly/yearly)
- `AIChat` — AI чатын session (auto-titled from first message)
- `AIMessage` — Чатын мессежүүд (user/assistant roles)

#### 3. Docker тохиргоо
- `backend/Dockerfile` — Multi-stage Go build (golang:1.23-alpine → alpine:3.19)
- `docker-compose.yml` — PostgreSQL 16 + Go backend
- PostgreSQL port: 5433 (host) → 5432 (container) — local PostgreSQL-тэй зөрчилдөхгүй
- Volume mount: postgres_data (persistent)
- Healthcheck: pg_isready

#### 4. Frontend (React Native) бүтээсэн
- **Framework:** Expo SDK 55 + React Native 0.83
- **Language:** TypeScript (strict mode, 0 tsc errors)
- **Styling:** NativeWind v4 (Tailwind CSS for React Native)
- **Navigation:** React Navigation v7 (native-stack + bottom-tabs)
- **HTTP Client:** Axios (typed, interceptors for JWT)
- **State:** React Context API (AuthContext)
- **Device:** Physical iPhone via USB (expo-dev-client)

**Үүсгэсэн дэлгэцүүд (11 screens):**
- `OnboardingScreen` — Welcome page with logo, "Get Started" button
- `LoginScreen` — Email + Password login
- `RegisterScreen` — Name + Email + Password registration
- `HomeScreen` — Balance, accounts, quick actions, recent transactions
- `TransactionsScreen` — Full history with search, tabs (All/Spending/Income)
- `ExpensesScreen` — Category breakdown, donut chart, period tabs
- `BudgetScreen` — Monthly budget progress bars, category budgets
- `StatisticsScreen` — Scheduled payments, income/expenses chart, period tabs
- `AIChatScreen` — AI financial advisor conversation interface
- `SettingsScreen` — Profile card, settings sections, logout
- `AddTransactionScreen` — Amount, type toggle, account/category selection

**Navigation бүтэц:**
- Auth Stack: Onboarding → Login / Register
- Main Bottom Tabs: Home | Analytics | (+)Add | AI Chat | Settings
- Modal: AddTransaction

**Дизайн (Figma-based dark theme):**
- Background: #0D0D0D
- Card: #1A1A2E
- Surface: #16213E
- Border: #2A2A3E
- Green: #00C853 (primary accent)
- Orange: #FF6B35 | Red: #FF4444 | Purple: #7C4DFF | Blue: #448AFF | Yellow: #FFD600

#### 5. Засагдсан алдаанууд (Bugs Fixed)
- go.mod XML error in VSCode → golang.go extension суулгасан
- Dockerfile Go version mismatch (1.22→1.23)
- docker-compose `version` attribute deprecated warning → removed
- DataGrip PostgreSQL port 5432→5433 (local conflict)
- Expo entry file → package.json main: "index.ts"
- CocoaPods stale cache → pods reinstall
- Xcode code signing → Personal Team + device trust
- NativeWind styles not rendering → babel.config.js jsxImportSource fix
- metro.config.ts → .js rename (Metro requirement)
- Registration failed → localhost→192.168.x.x (iPhone can't reach localhost)

---

## 2026-03-28 — Навигаци засвар + Activity Log (Day 2)

### Хийгдсэн ажлууд:

#### 1. Навигацийн алдаа засагдсан
**Асуудал:** HomeScreen болон SettingsScreen дахь бүх товчлуурууд дарахад юу ч болохгүй байсан — `onPress` handler байхгүй байсан.

**HomeScreen засварууд:**
- Transfer → AddTransaction screen-рүү navigate
- Cards → Accounts screen-рүү navigate
- Invest → Analytics tab-руу navigate
- Recurring → ScheduledPayments screen-рүү navigate
- More → Expenses screen-рүү navigate
- "View Details" → Expenses screen-рүү navigate
- Notification icon → Settings tab-руу navigate
- "See All" → Transactions (аль хэдийн ажиллаж байсан)

**SettingsScreen засварууд:**
- User card → Profile screen
- Profile → Profile screen
- General → Profile screen (currency тохиргоо)
- Account settings → Accounts screen
- Appearance → "Coming Soon" alert
- Data → "Coming Soon" alert
- Privacy → "Coming Soon" alert
- Help & Support → "Coming Soon" alert
- Rate App → "Coming Soon" alert
- Logout → Confirmation alert → logout (ажиллаж байсан)

#### 2. Шинэ дэлгэцүүд үүсгэсэн (3 screens)
- **ProfileScreen** — Нэр, email, валют засах (USD/EUR/MNT), member since
- **AccountsScreen** — Дансны жагсаалт, total balance, шинэ данс нэмэх (modal), устгах (long press)
- **ScheduledPaymentsScreen** — Давтагдах төлбөрийн жагсаалт, шинэ төлбөр нэмэх (modal), устгах

#### 3. Navigation шинэчлэгдсэн
- RootStackParamList-д шинэ screen-үүд нэмсэн: Profile, Accounts, ScheduledPayments
- AppNavigator Stack-д шинэ screen-үүд бүртгэсэн
- Нийт: 14 screen (11 → 14)

#### 4. Activity Log table нэмсэн (Backend)
**Шинэ model:** `ActivityLog`
- Fields: id, user_id, action, entity, entity_id, details (JSON), status (success/failed), ip_address, created_at
- Auto-migrate: database.go-д нэмсэн

**Шинэ handler:** `activity_log.go`
- `GET /activity-logs` — Paginated, action/entity/status filter
- `GET /activity-logs/summary` — Action тус бүрийн тоо

**Бүх handler-т logging нэмсэн:**
- auth.go → register, login
- transaction.go → create, delete
- budget.go → create, update, delete
- account.go → create, delete
- ai_chat.go → send message

#### 5. Баримт бичиг шинэчлэгдсэн
- README.md — Бүрэн шинэчлэгдсэн (34 routes, 9 models, setup guide)
- docs/work.md — Day 2 бүртгэл нэмсэн

#### 6. Git Repository
- master branch + develop branch үүсгэсэн
- Backend: Mnkh1212/Diploma-Backend
- Frontend: Mnkh1212/Diploma-Frontend

---

## Нийт статистик (Total Stats)

| Metric               | Count |
|----------------------|-------|
| Backend Go files     | 14    |
| API endpoints        | 34    |
| Database models      | 9     |
| Frontend screens     | 14    |
| TypeScript types     | 20+   |
| Default categories   | 17    |
| Docker services      | 2     |
| TSC errors           | 0     |

---

## Дараагийн алхамууд (Next Steps)
- [ ] AI chat-д external API (Claude/OpenAI) холбох
- [ ] Notification system нэмэх
- [ ] Data export/import feature
- [ ] Unit test бичих (Go + React Native)
- [ ] Performance optimization
- [ ] Appearance тохиргоо (light/dark theme toggle)
- [ ] Scheduled payments auto-execution (background job)
- [ ] Custom categories нэмэх
- [ ] Transaction update endpoint
- [ ] Charts сайжруулах (react-native-svg)
