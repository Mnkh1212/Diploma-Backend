"""FinTrack — Bank Statement Parser microservice.

FastAPI service that receives a bank statement file (PDF / Excel / CSV) and
returns a structured JSON describing the transactions, totals and category
breakdown. Mongolian bank formats (Khan, Golomt, TDB, Khas, State) are
recognised through keyword heuristics; unknown formats fall back to a generic
row scanner.

Endpoint:
    POST /parse  multipart form-data (file=...) -> ParsedStatement JSON

This service is purposefully stateless — the Go backend stores results.
"""

from __future__ import annotations

import io
import logging
import re
from collections import defaultdict
from dataclasses import dataclass, field
from datetime import datetime
from typing import Iterable, List, Optional, Tuple

import pandas as pd
import pdfplumber
from fastapi import FastAPI, File, HTTPException, UploadFile
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
log = logging.getLogger("parser")

app = FastAPI(title="FinTrack Statement Parser", version="1.0.0")


# ===================== Schema =====================


class ParsedTransaction(BaseModel):
    date: str = ""
    description: str = ""
    amount: float = 0.0
    type: str = "expense"
    category: str = "Бусад"
    balance: float = 0.0
    counterparty: str = ""  # Харьцсан этгээд (merchant нэр, дансан эзэмшигч)
    channel: str = ""  # POS, BOM, SocialPay, qpay, Transfer гэх мэт


class CategoryBreakdown(BaseModel):
    category: str
    amount: float
    percentage: float
    count: int


class CounterpartyBreakdown(BaseModel):
    counterparty: str
    amount: float
    count: int
    type: str  # income / expense
    avg_amount: float


class PatternInsight(BaseModel):
    kind: str  # "top_recipient", "large_single", "recurring", "channel_breakdown"
    title: str
    detail: str
    amount: float = 0.0
    count: int = 0


class ParsedStatement(BaseModel):
    bank_name: str = "Unknown Bank"
    opening_balance: float = 0.0
    closing_balance: float = 0.0
    total_income: float = 0.0
    total_expenses: float = 0.0
    period_start: str = ""
    period_end: str = ""
    transactions: List[ParsedTransaction] = []
    category_breakdown: List[CategoryBreakdown] = []
    counterparty_breakdown: List[CounterpartyBreakdown] = []
    patterns: List[PatternInsight] = []


# ===================== Helpers =====================


# Шимтгэл/хүү таних keyword-ууд — банкны үйлчилгээний шимтгэлийг бусдаас
# хамгийн түрүүн ялгана (Шимтгэл category нь POS/Transfer-ийн ангилалаас
# илүү онцлог).
FEE_DESC_KEYWORDS = (
    "charges for", "гүйлгээний шимтгэл", "данс хөтөлсний", "шимтгэл",
    "хураамж", "interest", "хүү", "fee",
)

# CATEGORY_RULES — нэгдсэн merchant нэр + үйл явдлын keyword-аар ангилал тогтоох.
# Channel-аас илүү тодорхой ангилал гаргахын тулд ЭХЭЛЖ шалгана (POS:KFCMONGOL
# нь "Хоол" болохоос "Картын гүйлгээ" биш).
CATEGORY_RULES: List[Tuple[str, List[str]]] = [
    ("Хоол", ["хоол", "ресторан", "кафе", "food", "restaurant", "kfc", "mcdonald", "pizza", "lotteria", "dino chic", "tsainii g", "kebab", "dessert"]),
    ("Такси", ["такси", "uber", "bolt", "taxi", "ubcab"]),
    ("Тээвэр", ["шатахуун", "petrol", "gas", "shell", "petrovis", "magicnet"]),
    ("Дэлгүүр", ["emart", "номин", "nomin", "minii", "минии", "store", "shop", "дэлгүүр", "circle k", "cu-", "gs25", "gs-25", "tesco", "tumen mal", "naran", "ikh mongo", "monos"]),
    ("Эрүүл мэнд", ["эмнэлэг", "pharmacy", "эмийн сан", "hospital", "clinic"]),
    ("Орон сууц", ["түрээс", "rent", "орон сууц", "ус сүлжээ", "халаалт"]),
    ("Интернет", ["unitel", "mobicom", "skytel", "gmobile", "интернет", "internet"]),
    ("Цалин", ["цалин", "salary", "tsalin", "wage"]),
    ("Зугаа цэнгэл", ["netflix", "spotify", "youtube", "tiktok", "subscription", "кино", "gar utas", "lotteria"]),
    ("Боловсрол", ["сургууль", "school", "tuition", "education", "boloвсрол", "course"]),
    ("Хадгаламж", ["хадгаламж", "deposit", "savings"]),
    ("Зээл", ["зээл", "loan", "credit"]),
]


def classify(desc: str, channel: str = "") -> str:
    """Гүйлгээний ангиллыг тодорхойлно.

    Priority:
      1. Шимтгэл (channel="Fee" эсвэл desc-д fee keyword) — хамгийн тодорхой
      2. Merchant нэр keyword (KFCMONGOL → Хоол, TESCO → Дэлгүүр)
      3. Channel-based (POS/BOM → Картын гүйлгээ, qpay → Цахим төлбөр,
         SocialPay/HappyPay/EB/Transfer → Шилжүүлэг)
      4. Бусад
    """
    low = (desc or "").lower()

    # 1. Шимтгэл — channel="Fee" эсвэл текстэд шимтгэлийн keyword
    if channel == "Fee" or any(k in low for k in FEE_DESC_KEYWORDS):
        return "Шимтгэл"

    # 2. Merchant-name keyword match (specific хоолны газар, дэлгүүр г.м.)
    for cat, keywords in CATEGORY_RULES:
        if any(k in low for k in keywords):
            return cat

    # 3. Channel-based generic fallback — merchant нэр танигдаагүй ч channel
    # мэдэгдэж байвал бүлэглэнэ
    if channel == "POS" or channel == "BOM":
        return "Картын гүйлгээ"
    if channel == "qpay":
        return "Цахим төлбөр"
    if channel in ("SocialPay", "HappyPay", "EB", "Transfer"):
        return "Шилжүүлэг"

    # 4. Description-аас "шилжүүлэг"/"transfer" keyword илрэх (channel-гүй tx-д)
    if "шилжүүлэг" in low or "transfer" in low:
        return "Шилжүүлэг"

    return "Бусад"


# ===================== Counterparty + channel extraction =====================
#
# Гүйлгээний тайлбараас хэн рүү / хэнээс гүйлгээ хийгдсэнийг шүүж олно.
# Жишээ:
#   "554835******2609:24-01-2026 12:24:31:POS:KFCMONGOL"
#     → counterparty="KFCMONGOL", channel="POS"
#   "SocialPay гүйлгээ,МӨНХЖАВХЛАН БАЯРЖАРГАЛ"
#     → counterparty="МӨНХЖАВХЛАН БАЯРЖАРГАЛ", channel="SocialPay"
#   "HAPPY PAY ГҮЙЛГЭЭ-МӨНХ-ЭРДЭНЭ БАТТҮВШИН"
#     → counterparty="МӨНХ-ЭРДЭНЭ БАТТҮВШИН", channel="HappyPay"
#   "qpay 770431279975287, 202602161110,АВТО"
#     → counterparty="АВТО", channel="qpay"
#   "Charges for PORD Customer Payment :000532547116"
#     → counterparty="", channel="Fee"

POS_RE = re.compile(r"POS:([A-Z0-9 ./\-]+?)(?:\s*\(Ханш|\s*$)", re.IGNORECASE)
BOM_RE = re.compile(r"BOM:([A-Z0-9 ./\-]+?)(?:\s*\(Ханш|\s*$)", re.IGNORECASE)
SOCIALPAY_RE = re.compile(r"socialpay\s+гүйлгээ[,，]\s*([^,，()]+)", re.IGNORECASE)
DASH_NAME_RE = re.compile(
    r"(?:ШИЛЖҮҮЛЭГ|HAPPY\s*PAY\s*ГҮЙЛГЭЭ|EB\s*-?[^-]*ИЛГЭЭВ|ГҮЙЛГЭЭ)\s*-+\s*(.+?)(?:\s*\(Ханш|$)",
    re.IGNORECASE,
)
QPAY_RE = re.compile(r"qpay\s+\d+[, ]+[\d\-A-Za-z]*[, ]+([^,()]+)", re.IGNORECASE)
NAME_BANK_RE = re.compile(
    r"^([А-ЯЁӨҮ\s\-\.]+(?:\s[А-ЯЁӨҮ\-\.]+)+?)\s*[,，]\s*([А-ЯЁӨҮ\s\-\.]+(?:БАНК|ТӨВ))",
    re.IGNORECASE,
)
FEE_KEYWORDS = (
    "charges for", "гүйлгээний шимтгэл", "данс хөтөлсний", "шимтгэл",
    "interest", "хүү", "fee",
)


def extract_counterparty(desc: str) -> Tuple[str, str]:
    """Description-аас counterparty + channel-ийг шүүж буцаана.

    Буцаах: (counterparty, channel).
    Хэрэв олдохгүй бол хоосон string-үүд.
    """
    if not desc:
        return "", ""

    text = desc.strip()
    low = text.lower()

    # Шимтгэл/хүү — counterparty үгүй, channel=Fee
    for kw in FEE_KEYWORDS:
        if kw in low:
            return "", "Fee"

    # POS payment (card swipe at merchant)
    m = POS_RE.search(text)
    if m:
        return _clean_name(m.group(1)), "POS"

    # BOM (Bank of Mongolia? — Голомтын мерчантын код)
    m = BOM_RE.search(text)
    if m:
        return _clean_name(m.group(1)), "BOM"

    # SocialPay
    m = SOCIALPAY_RE.search(text)
    if m:
        return _clean_name(m.group(1)), "SocialPay"

    # HappyPay / EB / Шилжүүлэг — dash-аар нэр салгасан
    m = DASH_NAME_RE.search(text)
    if m:
        channel = "Transfer"
        if "happy" in low:
            channel = "HappyPay"
        elif "eb" in low and "илгээв" in low:
            channel = "EB"
        elif "шилжүүлэг" in low:
            channel = "Transfer"
        return _clean_name(m.group(1)), channel

    # qpay
    m = QPAY_RE.search(text)
    if m:
        return _clean_name(m.group(1)), "qpay"
    if low.startswith("qpay"):
        return "", "qpay"

    # NAME, BANK form
    m = NAME_BANK_RE.search(text)
    if m:
        return _clean_name(m.group(1)), "Transfer"

    return "", ""


def _clean_name(name: str) -> str:
    if not name:
        return ""
    s = name.strip()
    # Trailing comma/dot-уудыг арилгана
    s = re.sub(r"[,，.\s]+$", "", s)
    # Олон зайтай бол ганц зай болгоно
    s = re.sub(r"\s+", " ", s)
    # Эхэнд тоо/цэг олонтой бол арилгана
    s = re.sub(r"^[\d.,\-:]+\s*", "", s)
    if len(s) > 60:
        s = s[:60]
    return s


def enrich(tx: ParsedTransaction) -> ParsedTransaction:
    """ParsedTransaction-ийг counterparty + channel + category-аар баяжуулна.

    Parser-аас гарсан tx нь description-аар л classified байсан. Channel мэдсэний
    дараа re-classify хийж илүү тодорхой ангилал (Шимтгэл / Картын гүйлгээ /
    Цахим төлбөр / Шилжүүлэг) гаргана.
    """
    if not tx.counterparty:
        cp, channel = extract_counterparty(tx.description)
        tx.counterparty = cp
        tx.channel = channel
    # Channel-based re-classify — desc + channel хосолно
    tx.category = classify(tx.description, tx.channel)
    return tx


BANK_KEYWORDS = [
    # Khan Bank: ХААН БАНК / KHAN BANK / XAAH БАНК (PDF text extraction-аас хамаарч)
    ("Khan Bank", ["khan bank", "khaan bank", "хаан банк", "xaah банк"]),
    ("Golomt Bank", ["golomt bank", "голомт банк", "голомт"]),
    # TDB — "tdb bank" эсвэл "trade and development" гэж тодорхой ярьсан үед л таних
    # (TDBM13361 гэх мэт transfer ID-аас зайлсхийнэ)
    ("TDB", ["tdb bank", "trade and development bank", "худалдаа хөгжлийн банк"]),
    ("Khas Bank", ["khas bank", "хас банк", "xacbank"]),
    ("State Bank", ["state bank", "төрийн банк"]),
    ("M Bank", ["m bank", "м банк"]),
    ("Capitron Bank", ["capitron bank", "капитрон банк"]),
    ("Arig Bank", ["arig bank", "ариг банк"]),
]


def detect_bank(text: str) -> str:
    low = text.lower()
    for name, keys in BANK_KEYWORDS:
        if any(k in low for k in keys):
            return name
    return "Unknown Bank"


# Жинхэнэ мөнгөн дүнг данс дугаараас (10+ оронтой) ялгахын тулд
# зөвхөн децимал цэгтэй тоог (1,234.00 / 5000.50) amount гэж үзнэ
AMOUNT_RE = re.compile(r"-?\(?\s*(?:\d{1,3}(?:[ ,']\d{3})+|\d{1,9})\.\d{1,2}\s*\)?")
# Loose: decimal-гүй жижиг тоог fallback болгон. 6-аас илүү оронтой бол data ID гэж үзэн алгасна.
AMOUNT_RE_LOOSE = re.compile(r"(?<!\d)-?\d{1,6}(?:[ ,']\d{3})?(?!\d)")
DATE_RE = re.compile(
    r"(\d{4}[-/.]\d{1,2}[-/.]\d{1,2}|\d{1,2}[-/.]\d{1,2}[-/.]\d{2,4})"
)


def normalize_amount(raw: str) -> Optional[float]:
    if not raw:
        return None
    s = raw.strip()
    neg = False
    if s.startswith("(") and s.endswith(")"):
        neg = True
        s = s[1:-1]
    if s.startswith("-"):
        neg = True
        s = s[1:]
    s = s.replace(" ", "").replace(" ", "").replace("'", "").replace("₮", "").replace("MNT", "")
    if s.count(",") and s.count("."):
        # 1,234.56 → 1234.56
        s = s.replace(",", "")
    elif s.count(",") and not s.count("."):
        # 1,234 → 1234 (mongolian thousand separator)
        # эсвэл 12,34 → 12.34 (decimal)
        if len(s.split(",")[-1]) == 2:
            s = s.replace(",", ".")
        else:
            s = s.replace(",", "")
    try:
        v = float(s)
    except ValueError:
        return None
    return -v if neg else v


def normalize_date(raw: str) -> str:
    if not raw:
        return ""
    raw = raw.strip()
    # Try ISO and DMY
    for fmt in ("%Y-%m-%d", "%Y/%m/%d", "%Y.%m.%d", "%d-%m-%Y", "%d/%m/%Y", "%d.%m.%Y", "%d-%m-%y", "%d/%m/%y"):
        try:
            return datetime.strptime(raw, fmt).strftime("%Y-%m-%d")
        except ValueError:
            continue
    return raw


# ===================== Parsers =====================


def parse_pdf(content: bytes) -> Tuple[str, List[ParsedTransaction], Optional[str]]:
    """Extract text + transactions from a PDF bank statement.

    Returns (text, transactions, bank_hint). bank_hint нь parser-ийн төрлөөс
    хамаарч bank_name-ийг override хийхэд хэрэглэнэ — PDF-ийн banner glyph-уудыг
    pdfplumber танихгүй байсан ч (жнь "ХААН БАНК" нь bold font-той учир `nnnn`-р
    гарч ирдэг) хүснэгтийн header-ээр банкийг тогтоох боломж олгоно.
    """
    text_parts: List[str] = []
    with pdfplumber.open(io.BytesIO(content)) as pdf:
        for page in pdf.pages:
            txt = page.extract_text() or ""
            text_parts.append(txt)
    text = "\n".join(text_parts)

    upper = text.upper()
    low = text.lower()

    # Khan Bank format-ыг түрүүлж шалгана. Хаан банк нь хүснэгт format-тай
    # (Эхний үлдэгдэл | Дебит | Кредит | Эцсийн үлдэгдэл) — ОРЛОГО/ЗАРЛАГА
    # keyword-гүй учир дараах Mongolian parser-аар уншиж чадахгүй.
    is_khan = (
        "хаан банк" in low
        or "khan bank" in low
        or "khaan bank" in low
        or ("эхний үлдэгдэл" in low and "дебит" in low and "кредит" in low)
    )
    if is_khan:
        khan_txs = parse_khan_format(content, text)
        if khan_txs:
            return text, khan_txs, "Khan Bank"

    # Голомт банк — хүснэгт format-тай (ОГНОО | АМ FC | АМ MNT | ТӨРӨЛ | УТГА).
    # Цахим гарын үсэгтэй PDF дээр extract_text нь баганыг хольж унших тохиолдол гардаг
    # тул table extraction-ыг урьтал ашиглана; үгүй бол Mongolian text parser fallback.
    is_golomt = "голомт" in low or "golomt" in low
    if is_golomt:
        g_txs = parse_golomt_format(content, text)
        if g_txs:
            return text, g_txs, "Golomt Bank"

    # Mongolian bank format-ыг шалгана. Хэрэв "ОРЛОГО" / "ЗАРЛАГА" cyrillic
    # keyword-ууд ихтэй бол Mongolian parser ашиглана — ингэснээр илүү нарийн.
    if upper.count("ОРЛОГО") + upper.count("ЗАРЛАГА") >= 4:
        txs = parse_mongolian_format(text)
        if txs:
            return text, txs, "Golomt Bank" if is_golomt else None

    # Generic table extraction
    txs: List[ParsedTransaction] = []
    with pdfplumber.open(io.BytesIO(content)) as pdf:
        for page in pdf.pages:
            for table in page.extract_tables() or []:
                if not table:
                    continue
                header = [str(c or "").strip().lower() for c in table[0]]
                for row in table[1:]:
                    tx = row_to_tx(header, row)
                    if tx:
                        txs.append(tx)

    # Last resort: line-based fallback
    if not txs:
        for line in text.splitlines():
            tx = line_to_tx(line)
            if tx:
                txs.append(tx)
    return text, txs, None


# Хаан банкны хуулга нь хүснэгт format. Багануудын тоо PDF-ээс хамаарч өөрчлөгдөнө,
# тиймээс header-ийн нэрээр баганыг олж унших нь хамгийн найдвартай.
KHAN_HEADER_HINTS = {
    "date": ["огноо"],
    "branch": ["салбар"],
    "open": ["эхний үлдэгдэл"],
    "debit": ["дебит"],
    "credit": ["кредит"],
    "close": ["эцсийн үлдэгдэл"],
    "desc": ["гүйлгээний утга", "утга"],
    "account": ["харьцсан данс", "данс"],
}


def _khan_col(header: List[str], hints: List[str]) -> Optional[int]:
    for idx, h in enumerate(header):
        for hint in hints:
            if hint in h:
                return idx
    return None


def parse_khan_format(content: bytes, text: str) -> List[ParsedTransaction]:
    """Хаан банкны хуулгад зориулсан parser.

    PDF-ийн хүснэгтийг pdfplumber-ийн extract_tables-аар уншиж, Дебит / Кредит
    баганыг тус тусад нь шалгана. Дебит > 0 → expense, Кредит > 0 → income.
    Хэрэв хоёулаа хоосон бол эцсийн – эхний үлдэгдлийн зөрүүгээр шийднэ.
    """
    txs: List[ParsedTransaction] = []
    seen_keys: set = set()

    try:
        with pdfplumber.open(io.BytesIO(content)) as pdf:
            for page in pdf.pages:
                for table in page.extract_tables() or []:
                    if not table or len(table) < 2:
                        continue
                    header = [str(c or "").strip().lower() for c in table[0]]
                    if not any("эхний үлдэгдэл" in h for h in header):
                        continue

                    col_date = _khan_col(header, KHAN_HEADER_HINTS["date"])
                    col_open = _khan_col(header, KHAN_HEADER_HINTS["open"])
                    col_debit = _khan_col(header, KHAN_HEADER_HINTS["debit"])
                    col_credit = _khan_col(header, KHAN_HEADER_HINTS["credit"])
                    col_close = _khan_col(header, KHAN_HEADER_HINTS["close"])
                    col_desc = _khan_col(header, KHAN_HEADER_HINTS["desc"])
                    col_account = _khan_col(header, KHAN_HEADER_HINTS["account"])

                    if col_open is None or col_close is None:
                        continue

                    for row in table[1:]:
                        if not row:
                            continue

                        def cell(i: Optional[int]) -> str:
                            if i is None or i >= len(row):
                                return ""
                            return str(row[i] or "").strip()

                        date_raw = cell(col_date)
                        # Огноогүй нэгтгэлийн мөрнүүд (нийт дүн г.м.)-ийг алгасна
                        if not re.search(r"\d{4}[-/.]\d{1,2}[-/.]\d{1,2}", date_raw):
                            continue

                        open_bal = normalize_amount(cell(col_open)) or 0.0
                        debit_raw = cell(col_debit) if col_debit is not None else ""
                        credit_raw = cell(col_credit) if col_credit is not None else ""
                        close_bal = normalize_amount(cell(col_close)) or 0.0

                        debit = abs(normalize_amount(debit_raw) or 0.0) if debit_raw else 0.0
                        credit = abs(normalize_amount(credit_raw) or 0.0) if credit_raw else 0.0

                        amount = 0.0
                        tx_type = "expense"
                        if credit > MIN_TX_AMOUNT:
                            amount = credit
                            tx_type = "income"
                        elif debit > MIN_TX_AMOUNT:
                            amount = debit
                            tx_type = "expense"
                        else:
                            diff = close_bal - open_bal
                            if abs(diff) < MIN_TX_AMOUNT:
                                continue
                            amount = abs(diff)
                            tx_type = "income" if diff > 0 else "expense"

                        desc = cell(col_desc) or "—"
                        account = cell(col_account) if col_account is not None else ""
                        if account and account not in desc:
                            desc = f"{desc} ({account})"
                        desc = re.sub(r"\s+", " ", desc).strip()[:200]

                        date = normalize_date(re.search(r"\d{4}[-/.]\d{1,2}[-/.]\d{1,2}", date_raw).group(0))
                        # Khan хуулгад нэг өдөрт ижил дүнтэй (жнь 100₮ хураамж)
                        # давтагдсан гүйлгээ ердийн зүйл — dedup хийхгүй.

                        txs.append(ParsedTransaction(
                            date=date,
                            description=desc,
                            amount=amount,
                            type=tx_type,
                            category=classify(desc),
                            balance=close_bal,
                        ))
    except Exception as exc:  # noqa: BLE001
        log.warning("khan table parse failed: %s", exc)

    # Текстээс ч давхар уншаад илүү гүйлгээ олдсон тал руу нь сонгоно. PDF
    # хүснэгтэд multi-line cells/page break-ээс шалтгаалж зарим мөр алддагтай
    # уялдан илүү найдвартай.
    text_txs = _parse_khan_text(text)
    if len(text_txs) > len(txs):
        return text_txs
    return txs or text_txs


KHAN_ROW_RE = re.compile(
    r"^\s*\d+\s+(\d{4}/\d{2}/\d{2})\s+\d{1,2}:\d{2}\s+(\d{4})\s+(.+)$"
)


def _parse_khan_text(text: str) -> List[ParsedTransaction]:
    txs: List[ParsedTransaction] = []
    for raw_line in text.splitlines():
        m = KHAN_ROW_RE.match(raw_line)
        if not m:
            continue
        date = normalize_date(m.group(1))
        rest = m.group(3)
        amounts = AMOUNT_RE.findall(rest)
        if len(amounts) < 2:
            continue
        # 2 эсвэл 3 дүн байж болно. Хамгийн утга учиртай нь:
        #   - 3 байвал: open, debit_or_credit, close (debit нь "-" prefix-тэй байдаг)
        #   - 2 байвал: open, close (no movement) — энэ үед алгасна
        nums = [normalize_amount(a) for a in amounts[:3]]
        nums = [n for n in nums if n is not None]
        if len(nums) < 2:
            continue
        if len(nums) >= 3:
            open_bal = nums[0]
            mid = nums[1]
            close_bal = nums[2]
            # Дэлэгрэнгүй: open ба close-ийн зөрүү нь mid-ийн абсолют утгатай ойролцоо байх ёстой
            diff = close_bal - open_bal
            amount = abs(mid)
            tx_type = "income" if diff > 0 else "expense"
            # Хэрэв зөрүү бараг 0 бол mid-ийн тэмдгээр тодорхойлно
            if abs(diff) < 1:
                continue
        else:
            open_bal = nums[0]
            close_bal = nums[1]
            diff = close_bal - open_bal
            if abs(diff) < MIN_TX_AMOUNT:
                continue
            amount = abs(diff)
            tx_type = "income" if diff > 0 else "expense"

        if amount < MIN_TX_AMOUNT:
            continue

        # Тайлбар — сүүлийн тооны араас
        desc = rest
        for a in amounts[:3]:
            desc = desc.replace(a, "", 1)
        desc = re.sub(r"\s+", " ", desc).strip(" -|") or "—"

        txs.append(ParsedTransaction(
            date=date,
            description=desc[:200],
            amount=amount,
            type=tx_type,
            category=classify(desc),
            balance=close_bal,
        ))
    return txs


# ===================== Golomt parser =====================

# Golomt-ийн жинхэнэ гүйлгээний мөр: "<amount> ОРЛОГО|ЗАРЛАГА <description>"
# (амжилт нь "ӨДРИЙН"-ээр эхэлдэггүй ба "НИЙТ"-ээс өмнө биш).
GOLOMT_TX_RE = re.compile(
    r"^\s*([\d,'\s]+\.\d{1,2})\s+(ОРЛОГО|ЗАРЛАГА)\s+(.+?)$"
)
GOLOMT_DATE_ONLY_RE = re.compile(r"^\s*(\d{4}[-/.]\d{1,2}[-/.]\d{1,2})\s*$")
GOLOMT_SKIP_TOKENS = (
    "ӨДРИЙН ОРЛОГО",
    "ӨДРИЙН ЗАРЛАГА",
    "ӨДРИЙН ҮЛДЭГДЭЛ",
    "ЭХНИЙ ҮЛДЭГДЭЛ",
    "ЭЦСИЙН ҮЛДЭГДЭЛ",
    "НИЙТ ОРЛОГО",
    "НИЙТ ЗАРЛАГА",
    "НИЙТ КРЕДИТ",
    "НИЙТ ДЕБИТ",
)


def parse_golomt_format(content: bytes, text: str) -> List[ParsedTransaction]:
    """Голомт банкны хуулга — хоёр шаттай parser.

    1. pdfplumber.extract_tables-аар хүснэгт уншиж нарийн ажиллах эсэхийг үзнэ.
       Хүснэгтийн багана: ОГНОО | ВАЛЮТ FC | MNT | ТӨРӨЛ | ГҮЙЛГЭЭНИЙ УТГА.
    2. Хүснэгтээс гүйлгээ олдоогүй бол text-ийг мөр-мөрөөр уншиж "AMOUNT ОРЛОГО|ЗАРЛАГА DESC"
       хэв загвартай мөрийг л зөвшөөрнө. Энэ нь pdfplumber-ийн extract_text-ээс
       багана хольж бичсэн алдааг шүүж хаяна.
    """
    table_txs = _parse_golomt_tables(content)
    text_txs = _parse_golomt_text(text)
    # Илүү олон бөгөөд "Бусад"-аас өөр ангилалтай гарсан тал руу нь сонгоно.
    if len(table_txs) >= len(text_txs) and table_txs:
        return table_txs
    return text_txs


def _parse_golomt_tables(content: bytes) -> List[ParsedTransaction]:
    txs: List[ParsedTransaction] = []
    current_date = ""
    try:
        with pdfplumber.open(io.BytesIO(content)) as pdf:
            for page in pdf.pages:
                for table in page.extract_tables() or []:
                    if not table:
                        continue
                    for row in table:
                        if not row:
                            continue
                        cells = [str(c or "").strip() for c in row]
                        if not any(cells):
                            continue
                        joined = " ".join(cells).upper()

                        # Огноо ганцаараа байгаа мөр
                        for c in cells:
                            m = GOLOMT_DATE_ONLY_RE.match(c)
                            if m and not any(
                                u in joined for u in ("ОРЛОГО", "ЗАРЛАГА", "ҮЛДЭГДЭЛ")
                            ):
                                current_date = normalize_date(m.group(1))
                                break

                        # Өдрийн нэгтгэл/нийт мөр — алгасна
                        if any(skip in joined for skip in GOLOMT_SKIP_TOKENS):
                            continue

                        # Type cell
                        tx_type: Optional[str] = None
                        for c in cells:
                            up = c.upper().strip()
                            if up == "ОРЛОГО":
                                tx_type = "income"
                                break
                            if up == "ЗАРЛАГА":
                                tx_type = "expense"
                                break
                        if not tx_type:
                            continue

                        # Дансны үлдэгдэл биш бодит мөнгөн дүн (decimal-тай, 100M-ээс бага)
                        amount = 0.0
                        for c in cells:
                            v = normalize_amount(c)
                            if v is None:
                                continue
                            av = abs(v)
                            if 100 <= av <= 100_000_000:
                                amount = av
                                break
                        if amount < 100:
                            continue

                        # Description — амжилт ба огноо-биш хамгийн урт ячейка
                        desc = ""
                        for c in cells:
                            if not c:
                                continue
                            up = c.upper()
                            if up in ("ОРЛОГО", "ЗАРЛАГА"):
                                continue
                            if GOLOMT_DATE_ONLY_RE.match(c):
                                continue
                            if normalize_amount(c) is not None:
                                continue
                            if len(c) > len(desc):
                                desc = c
                        desc = re.sub(r"\s*\(Ханш:[^)]*\)?\s*$", "", desc).strip()
                        desc = re.sub(r"\s+", " ", desc)[:200] or "—"

                        # Огноог row-оос эсвэл өмнөх мөрнөөс
                        date_in_row = ""
                        for c in cells:
                            m = GOLOMT_DATE_ONLY_RE.match(c)
                            if m:
                                date_in_row = normalize_date(m.group(1))
                                break
                        date = date_in_row or current_date

                        txs.append(ParsedTransaction(
                            date=date,
                            description=desc,
                            amount=amount,
                            type=tx_type,
                            category=classify(desc),
                        ))
    except Exception as exc:  # noqa: BLE001
        log.warning("golomt table parse failed: %s", exc)
    return txs


def _parse_golomt_text(text: str) -> List[ParsedTransaction]:
    txs: List[ParsedTransaction] = []
    current_date = ""
    pending: Optional[ParsedTransaction] = None

    for raw_line in text.splitlines():
        line = raw_line.strip()
        if not line:
            pending = None
            continue
        upper = line.upper()

        # Өдрийн/нийт нэгтгэл — алгасна
        if any(sk in upper for sk in GOLOMT_SKIP_TOKENS):
            pending = None
            continue

        # Огноо ганцаараа
        m = GOLOMT_DATE_ONLY_RE.match(line)
        if m:
            current_date = normalize_date(m.group(1))
            pending = None
            continue

        # "AMOUNT ОРЛОГО|ЗАРЛАГА DESC" хэв загварт яг таарсан мөр л
        m = GOLOMT_TX_RE.match(line)
        if m:
            amount = normalize_amount(m.group(1))
            tx_type = "income" if m.group(2).upper() == "ОРЛОГО" else "expense"
            desc = m.group(3).strip()
            desc = re.sub(r"\s*\(Ханш:[^)]*\)?\s*$", "", desc).strip()

            if amount is None or abs(amount) < 100 or abs(amount) > 100_000_000:
                pending = None
                continue

            tx = ParsedTransaction(
                date=current_date,
                description=desc[:200] or "—",
                amount=abs(amount),
                type=tx_type,
                category=classify(desc),
            )
            txs.append(tx)
            pending = tx
            continue

        # Multi-line description — өмнөх tx-ийн description-д залгана.
        # Зөвхөн эхлэлд нь тоо/амжилт байхгүй, бас skip-keyword-гүй мөр л залгана.
        if pending and not _looks_like_data_row(line):
            new_desc = (pending.description + " " + line).strip()[:200]
            pending.description = new_desc
            pending.category = classify(new_desc)

    return txs


# Монгол банкны хуулга-д тааруулсан parser. Голомт зэрэг банкны formats:
#
#     2026-01-24
#     50,000.00   ОРЛОГО     ШИЛЖҮҮЛЭГ-МӨНХ-ЭРДЭНЭ ...
#         500.00   ЗАРЛАГА    Данс хөтөлсний шимтгэл ...
#     50,000.00            ӨДРИЙН ОРЛОГО         <- алгасна
#        500.00            ӨДРИЙН ЗАРЛАГА        <- алгасна
#     54,905.50            ӨДРИЙН ҮЛДЭГДЭЛ      <- алгасна
#
# Дүн нь keyword-ээс өмнө байх онцлогтой.

DAILY_SKIP_KEYWORDS = (
    "ӨДРИЙН ОРЛОГО",
    "ӨДРИЙН ЗАРЛАГА",
    "ӨДРИЙН ҮЛДЭГДЭЛ",
)

DATE_LINE_RE = re.compile(r"^\s*(\d{4}[-/.]\d{1,2}[-/.]\d{1,2}|\d{1,2}[-/.]\d{1,2}[-/.]\d{2,4})\s*$")


def parse_mongolian_format(text: str) -> List[ParsedTransaction]:
    txs: List[ParsedTransaction] = []
    current_date = ""
    pending: Optional[ParsedTransaction] = None  # multi-line description-ыг үргэлжлүүлэхэд

    for raw_line in text.splitlines():
        line = raw_line.strip()
        if not line:
            pending = None
            continue

        # Огнооны мөр (зөвхөн огноо ганцаараа)
        m = DATE_LINE_RE.match(line)
        if m:
            current_date = normalize_date(m.group(1))
            pending = None
            continue

        upper = line.upper()

        # Өдрийн нэгтгэлийн мөр — алгасна
        if any(sk in upper for sk in DAILY_SKIP_KEYWORDS):
            pending = None
            continue

        # Нийт болон эхний/эцсийн үлдэгдлийн мөр (parse_statement дотор тус
        # extract_summary_amounts-аар авна) — энд алгасна.
        if (
            "НИЙТ ОРЛОГО" in upper
            or "НИЙТ ЗАРЛАГА" in upper
            or "ЭХНИЙ ҮЛДЭГДЭЛ" in upper
            or "ЭЦСИЙН ҮЛДЭГДЭЛ" in upper
        ):
            pending = None
            continue

        # Гүйлгээний мөр — keyword "ОРЛОГО" эсвэл "ЗАРЛАГА" агуулна
        tx_type: Optional[str] = None
        keyword: Optional[str] = None
        # ЗАРЛАГА-г эхэнд шалгах (income/expense зөв ялгахын тулд)
        if "ЗАРЛАГА" in upper:
            tx_type = "expense"
            keyword = "ЗАРЛАГА"
        elif "ОРЛОГО" in upper:
            tx_type = "income"
            keyword = "ОРЛОГО"

        if tx_type and keyword:
            kw_idx = upper.index(keyword)
            before = line[:kw_idx]
            after = line[kw_idx + len(keyword):].strip()

            # before хэсгээс хамгийн сүүлийн (хамгийн том) тоог дүн гэж авна
            amounts = AMOUNT_RE.findall(before)
            picked: Optional[float] = None
            for a in reversed(amounts):
                v = normalize_amount(a)
                if v is not None and abs(v) >= 100:
                    picked = abs(v)
                    break
            if picked is None:
                pending = None
                continue

            tx = ParsedTransaction(
                date=current_date,
                description=after if after else "—",
                amount=picked,
                type=tx_type,
                category=classify(after),
            )
            txs.append(tx)
            pending = tx
            continue

        # Үргэлжилсэн тайлбарын мөр (амжилт нь сөрөг ёсоор):
        # Хэрэв өмнөх pending tx байгаа бөгөөд энэ мөр нь "ОРЛОГО"/"ЗАРЛАГА"
        # биш бол tx-ийн description-д залгана.
        if pending and not _looks_like_data_row(line):
            pending.description = (pending.description + " " + line).strip()
            pending.category = classify(pending.description)

    return txs


def _looks_like_data_row(line: str) -> bool:
    """Тоогоор эхэлдэг мөр уу? (өөр гүйлгээ эхэлж магадгүй)"""
    s = line.strip()
    if not s:
        return False
    return bool(re.match(r"^[\d\s,'.]+$", s.split()[0]))


# Banks summary line хайх. "НИЙТ ОРЛОГО: 4,662,900.00" гэх мэт.
SUMMARY_PATTERNS: List[Tuple[str, List[str]]] = [
    ("total_income", [
        r"нийт\s*орлого",
        r"total\s*income",
        r"total\s*credit",
        r"нийт\s*кредит",
        r"нийт\s*кредит\s*гүйлгээ",
    ]),
    ("total_expense", [
        r"нийт\s*зарлага",
        r"total\s*expense",
        r"total\s*debit",
        r"нийт\s*дебет",
        r"нийт\s*дебит",
        r"нийт\s*дебит\s*гүйлгээ",
    ]),
    ("opening_balance", [
        r"эхний\s*үлдэгдэл",
        r"opening\s*balance",
        r"тайлант\s*үеийн\s*эхний",
        r"эхний\s*\(.*?\)\s*үлдэгдэл",
        r"эхний\s*дансны\s*үлдэгдэл",
    ]),
    ("closing_balance", [
        r"эцсийн\s*үлдэгдэл",
        r"closing\s*balance",
        r"эцэст\s*\(.*?\)\s*үлдэгдэл",
        r"үлдэгдэл\s*эцэст",
        r"эцсийн\s*дансны\s*үлдэгдэл",
    ]),
]


def extract_summary_amounts(raw: str) -> dict:
    """PDF/Excel-ийн text дотроос 'НИЙТ ОРЛОГО', 'НИЙТ ЗАРЛАГА', 'Эхний/Эцсийн үлдэгдэл'
    гэх мэт мөрнөөс жинхэнэ тоог олж буцаана.

    Багана хольж бичдэг банкуудын алдаанаас сэргийлэхийн тулд:
      - Зөвхөн keyword-той ИЖИЛ мөрөн дэх amount-ыг авна
      - Сүүлээс эхлэн (footer) скан хийнэ — НИЙТ ОРЛОГО/ЗАРЛАГА ихэвчлэн доор байдаг
      - Голомт стиль: "4,662,900.00 НИЙТ ОРЛОГО" (amount урьдчилан бичигдсэн)
        / Бусад: "ENDING BALANCE: 100.00" (amount хойноос)
    """
    out: dict = {}
    if not raw:
        return out
    lines = raw.splitlines()

    for key, patterns in SUMMARY_PATTERNS:
        for line in reversed(lines):
            low_line = line.lower()
            matched = None
            for pat in patterns:
                m = re.search(pat, low_line)
                if m:
                    matched = m
                    break
            if not matched:
                continue

            head = line[: matched.start()]
            tail = line[matched.end():]
            picked: Optional[float] = None

            # Голомт стиль: amount keyword-ийн өмнө
            for a in reversed(AMOUNT_RE.findall(head)):
                v = normalize_amount(a)
                if v is not None and 100 <= abs(v) <= 1_000_000_000:
                    picked = abs(v)
                    break

            # Эсвэл хойноос
            if picked is None:
                for a in AMOUNT_RE.findall(tail):
                    v = normalize_amount(a)
                    if v is not None and 100 <= abs(v) <= 1_000_000_000:
                        picked = abs(v)
                        break

            if picked is not None:
                out[key] = picked
                break
    return out


def parse_excel(content: bytes, ext: str) -> Tuple[str, List[ParsedTransaction]]:
    engine = None
    if ext == ".xls":
        engine = "xlrd"
    elif ext == ".xlsx":
        engine = "openpyxl"
    try:
        sheets = pd.read_excel(io.BytesIO(content), sheet_name=None, engine=engine, dtype=str)
    except Exception as exc:  # noqa: BLE001
        log.warning("excel read failed: %s", exc)
        return "", []

    txs: List[ParsedTransaction] = []
    text_parts: List[str] = []
    for _, df in sheets.items():
        df = df.fillna("")
        text_parts.append(df.to_csv(index=False, sep="|"))
        if df.empty:
            continue
        header = [str(c).strip().lower() for c in df.columns]
        for _, row in df.iterrows():
            tx = row_to_tx(header, [str(v) for v in row.tolist()])
            if tx:
                txs.append(tx)
    return "\n".join(text_parts), txs


def parse_csv(content: bytes) -> Tuple[str, List[ParsedTransaction]]:
    try:
        df = pd.read_csv(io.BytesIO(content), dtype=str)
    except Exception:
        try:
            df = pd.read_csv(io.BytesIO(content), dtype=str, sep=";")
        except Exception as exc:  # noqa: BLE001
            log.warning("csv read failed: %s", exc)
            return "", []
    df = df.fillna("")
    header = [str(c).strip().lower() for c in df.columns]
    txs = []
    for _, row in df.iterrows():
        tx = row_to_tx(header, [str(v) for v in row.tolist()])
        if tx:
            txs.append(tx)
    return df.to_csv(index=False, sep="|"), txs


# Header tokens → field name
HEADER_HINTS = {
    "date": ["date", "огноо", "гүйлгээний огноо", "transaction date", "огноо/цаг"],
    "description": ["description", "narrative", "тайлбар", "гүйлгээний утга", "purpose", "детайл", "details"],
    "credit": ["credit", "орлого", "deposit", "in"],
    "debit": ["debit", "зарлага", "withdrawal", "out"],
    "amount": ["amount", "дүн", "value"],
    "balance": ["balance", "үлдэгдэл"],
    "type": ["type", "төрөл"],
}


def header_index(header: List[str], hints: List[str]) -> Optional[int]:
    for idx, h in enumerate(header):
        for hint in hints:
            if hint in h:
                return idx
    return None


def row_to_tx(header: List[str], row: List[str]) -> Optional[ParsedTransaction]:
    if not row or all(not (c or "").strip() for c in row):
        return None

    idx_date = header_index(header, HEADER_HINTS["date"])
    idx_desc = header_index(header, HEADER_HINTS["description"])
    idx_credit = header_index(header, HEADER_HINTS["credit"])
    idx_debit = header_index(header, HEADER_HINTS["debit"])
    idx_amount = header_index(header, HEADER_HINTS["amount"])
    idx_balance = header_index(header, HEADER_HINTS["balance"])

    def get(i: Optional[int]) -> str:
        if i is None or i >= len(row):
            return ""
        return (row[i] or "").strip()

    date = normalize_date(get(idx_date))
    desc = get(idx_desc)
    balance = normalize_amount(get(idx_balance)) or 0.0

    amount = 0.0
    tx_type = "expense"

    if idx_credit is not None and get(idx_credit):
        v = normalize_amount(get(idx_credit))
        if v and v > 0:
            amount = v
            tx_type = "income"
    if idx_debit is not None and get(idx_debit) and amount == 0:
        v = normalize_amount(get(idx_debit))
        if v and v != 0:
            amount = abs(v)
            tx_type = "expense"
    if amount == 0 and idx_amount is not None and get(idx_amount):
        v = normalize_amount(get(idx_amount))
        if v is not None:
            amount = abs(v)
            tx_type = "income" if v > 0 else "expense"

    if amount == 0 and not date and not desc:
        return None
    if amount == 0:
        return None

    if not desc:
        # Бүх багана нэгтгэе
        desc = " ".join(c for c in row if c and c.strip())[:140]

    return ParsedTransaction(
        date=date,
        description=desc.strip(),
        amount=amount,
        type=tx_type,
        category=classify(desc),
        balance=balance,
    )


def line_to_tx(line: str) -> Optional[ParsedTransaction]:
    line = line.strip()
    if len(line) < 8:
        return None

    date_match = DATE_RE.search(line)
    if not date_match:
        return None
    date = normalize_date(date_match.group(0))

    amounts = [m.group(0) for m in AMOUNT_RE.finditer(line)]
    # Сүүлийн valid тоог дүн гэж үзнэ
    amount: Optional[float] = None
    for a in reversed(amounts):
        v = normalize_amount(a)
        if v is not None and abs(v) > 0:
            amount = v
            break
    if amount is None:
        return None

    tx_type = "expense"
    low = line.lower()
    if amount > 0 and ("credit" in low or "+" in line.split(date_match.group(0))[1][:5]):
        tx_type = "income"
    if amount < 0 or "debit" in low:
        tx_type = "expense"

    desc = (
        line.replace(date_match.group(0), "", 1)
        .strip()
    )
    for a in amounts:
        desc = desc.replace(a, "")
    desc = re.sub(r"\s+", " ", desc).strip(" -|")
    if not desc:
        desc = "—"

    return ParsedTransaction(
        date=date,
        description=desc[:140],
        amount=abs(amount),
        type=tx_type,
        category=classify(desc),
    )


# ===================== Aggregation =====================


MIN_TX_AMOUNT = 100.0  # 100₮-өөс бага noise-уудыг хаяна
MAX_TX_AMOUNT = 100_000_000.0  # 100 саяаас дээш — данс дугаар / parser алдаа гэж үзнэ
MAX_TOTAL_AMOUNT = 1_000_000_000.0  # Нэгтгэл дүн (нийт орлого/зарлага) хязгаар


def filter_noise(transactions: List[ParsedTransaction]) -> List[ParsedTransaction]:
    """Page numbers, мөрийн дугаар, date-ийн хагас, тайлбар хоосон тоонуудыг хаяна.

    Sanity check: 100 саяаас дээш дүнтэй "гүйлгээ" нь хувийн дансанд бараг үргэлж
    данс дугаарыг decimal-тэйгээр (5,876,157,584.00) parse хийж буруу таасан алдаа.
    Хэрэв legitimate бизнес дансны хуулга бол энэ хязгаарыг өөрчилнө.
    """
    cleaned: List[ParsedTransaction] = []
    for t in transactions:
        if t.amount < MIN_TX_AMOUNT or t.amount > MAX_TX_AMOUNT:
            continue
        desc = (t.description or "").strip()
        # Тайлбар нь зөвхөн тоо/огноо/хоосон бол алгасна
        if not desc or desc in {"—", "-"}:
            continue
        if re.fullmatch(r"[\d\s\-/.,]+", desc):
            continue
        cleaned.append(t)
    return cleaned


def aggregate(transactions: List[ParsedTransaction]) -> Tuple[float, float, List[CategoryBreakdown], str, str]:
    income = sum(t.amount for t in transactions if t.type == "income")
    expense = sum(t.amount for t in transactions if t.type == "expense")

    totals = defaultdict(float)
    counts = defaultdict(int)
    for t in transactions:
        if t.type != "expense":
            continue
        totals[t.category] += t.amount
        counts[t.category] += 1

    grand = sum(totals.values()) or 1.0
    breakdown = sorted(
        [
            CategoryBreakdown(
                category=cat,
                amount=amt,
                percentage=(amt / grand) * 100,
                count=counts[cat],
            )
            for cat, amt in totals.items()
        ],
        key=lambda c: c.amount,
        reverse=True,
    )

    dates = sorted([t.date for t in transactions if t.date])
    period_start = dates[0] if dates else ""
    period_end = dates[-1] if dates else ""

    return income, expense, breakdown, period_start, period_end


def aggregate_counterparties(
    transactions: List[ParsedTransaction],
) -> List[CounterpartyBreakdown]:
    """Counterparty + type-аар нэгтгэж зарцуулалт/орлогыг харуулна."""
    totals: dict = defaultdict(lambda: {"amount": 0.0, "count": 0, "type": ""})
    for t in transactions:
        if not t.counterparty:
            continue
        key = (t.counterparty, t.type)
        totals[key]["amount"] += t.amount
        totals[key]["count"] += 1
        totals[key]["type"] = t.type

    out: List[CounterpartyBreakdown] = []
    for (cp, _typ), data in totals.items():
        out.append(
            CounterpartyBreakdown(
                counterparty=cp,
                amount=data["amount"],
                count=data["count"],
                type=data["type"],
                avg_amount=data["amount"] / data["count"] if data["count"] > 0 else 0.0,
            )
        )
    out.sort(key=lambda c: c.amount, reverse=True)
    return out


def detect_patterns(
    transactions: List[ParsedTransaction],
    counterparties: List[CounterpartyBreakdown],
) -> List[PatternInsight]:
    """Гүйлгээний өгөгдөлд анхаарал татах хэв шинжийг олно.

    - Хамгийн их зарцуулсан мерчант
    - Хамгийн их 1 удаагийн зарлага
    - Давтагдсан гүйлгээ (3+ удаа ижил мерчант руу)
    - Channel breakdown (POS vs SocialPay vs HappyPay г.м)
    """
    patterns: List[PatternInsight] = []

    # 1. Top expense counterparty
    expenses = [c for c in counterparties if c.type == "expense"]
    if expenses:
        top = expenses[0]
        if top.amount >= 50_000 and top.count >= 2:
            patterns.append(
                PatternInsight(
                    kind="top_recipient",
                    title=f"Хамгийн их зарцуулсан газар: {top.counterparty}",
                    detail=f"{top.count} удаа гүйлгээ, нийт {fmt_amt(top.amount)}₮, дунджаар {fmt_amt(top.avg_amount)}₮",
                    amount=top.amount,
                    count=top.count,
                )
            )

    # 2. Largest single expense
    expense_txs = [t for t in transactions if t.type == "expense"]
    if expense_txs:
        largest = max(expense_txs, key=lambda t: t.amount)
        if largest.amount >= 200_000:
            who = largest.counterparty or largest.description[:40] or "—"
            patterns.append(
                PatternInsight(
                    kind="large_single",
                    title="Том дүнтэй гүйлгээ",
                    detail=f"{fmt_amt(largest.amount)}₮ — {who}",
                    amount=largest.amount,
                    count=1,
                )
            )

    # 3. Recurring small expenses (subscription-маягийн зүйл — давтагдсан жижиг гүйлгээ)
    for c in expenses:
        if c.count >= 3 and c.avg_amount < 50_000:
            patterns.append(
                PatternInsight(
                    kind="recurring",
                    title=f"Давтагдсан зарлага: {c.counterparty}",
                    detail=f"{c.count} удаа х дунджаар {fmt_amt(c.avg_amount)}₮ = нийт {fmt_amt(c.amount)}₮",
                    amount=c.amount,
                    count=c.count,
                )
            )
        if len(patterns) >= 6:
            break

    # 4. Channel breakdown
    channel_totals: dict = defaultdict(lambda: {"amount": 0.0, "count": 0})
    for t in transactions:
        if t.type != "expense" or not t.channel:
            continue
        channel_totals[t.channel]["amount"] += t.amount
        channel_totals[t.channel]["count"] += 1
    if channel_totals:
        sorted_channels = sorted(channel_totals.items(), key=lambda kv: kv[1]["amount"], reverse=True)
        top_channel, top_data = sorted_channels[0]
        if top_data["count"] >= 3:
            patterns.append(
                PatternInsight(
                    kind="channel_breakdown",
                    title=f"Гол төлбөрийн арга: {top_channel}",
                    detail=f"{top_data['count']} гүйлгээ, нийт {fmt_amt(top_data['amount'])}₮",
                    amount=top_data["amount"],
                    count=top_data["count"],
                )
            )

    return patterns


def fmt_amt(v: float) -> str:
    """Мөнгөн дүнг ',' separator-той форматлана. (Помощник pattern detail-д.)"""
    return f"{int(round(v)):,}"


def derive_balances(transactions: List[ParsedTransaction], income: float, expense: float) -> Tuple[float, float]:
    """Хэрэв гүйлгээний balance байхгүй бол last/first дэх balance-ыг тооцоолно."""
    balances = [t.balance for t in transactions if t.balance]
    if not balances:
        return 0.0, income - expense
    return balances[0] - (income_of_first(transactions, "income") - income_of_first(transactions, "expense")), balances[-1]


def income_of_first(_transactions: List[ParsedTransaction], _type: str) -> float:
    # placeholder helper — opening balance цогц тооцооллыг service хэрэглэгчид өгөхгүй,
    # statement-аас direct-аар read-лж чадаагүй үед opening = 0 гэж үзнэ.
    return 0.0


# ===================== Endpoints =====================


@app.get("/")
def root():
    return {"service": "FinTrack Parser", "version": "1.0.0", "endpoints": ["/parse"]}


@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/parse", response_model=ParsedStatement)
async def parse_statement(file: UploadFile = File(...)):
    name = (file.filename or "").lower()
    content = await file.read()
    if not content:
        raise HTTPException(status_code=400, detail="Empty file")

    bank_hint: Optional[str] = None
    if name.endswith(".pdf"):
        text, txs, bank_hint = parse_pdf(content)
    elif name.endswith(".xlsx") or name.endswith(".xls"):
        ext = ".xlsx" if name.endswith(".xlsx") else ".xls"
        text, txs = parse_excel(content, ext)
    elif name.endswith(".csv"):
        text, txs = parse_csv(content)
    else:
        raise HTTPException(status_code=400, detail="Зөвхөн PDF, Excel, CSV дэмжинэ")

    # PDF banner glyph (жнь "ХААН БАНК" bold font-той учир `nnnn`-р гарч ирэх)
    # detect_bank-аар таних боломжгүй. Parser-ийн тодорхойлсон bank_hint-ийг урьтал.
    bank_name = bank_hint or detect_bank(text or "")
    txs = filter_noise(txs)

    # Counterparty + channel оноох (description-аас merchant/recipient шүүх).
    # Бүх parse_*_format-ууд description өгсөн байх ёстой.
    for t in txs:
        enrich(t)

    # Эхлээд гүйлгээний нийлбэрээр тооцно...
    income, expense, breakdown, period_start, period_end = aggregate(txs)

    # ...дараа нь PDF/Excel-ийн "НИЙТ ОРЛОГО / ЗАРЛАГА / Үлдэгдэл" мөрийг
    # хайж олж, түүгээр нь override хийнэ. Энэ нь хэдэн зуун мөрийн нэгтгэлээс
    # илүү найдвартай.
    summary = extract_summary_amounts(text or "")
    if "total_income" in summary:
        income = summary["total_income"]
    if "total_expense" in summary:
        expense = summary["total_expense"]

    # Khan Bank footer: "Нийт дүн: <debit> <credit>" гэсэн форматтай
    # (debit = expense, credit = income). pdfplumber-аас гарч ирэх ялгаа: tab,
    # multiple spaces, NBSP, эсвэл шинэ мөрөөр salgaмаагдаж болно.
    if bank_hint == "Khan Bank":
        khan_total = re.search(
            r"нийт\s*дүн[\s:]*("
            + AMOUNT_RE.pattern
            + r")[\s ]+("
            + AMOUNT_RE.pattern
            + r")",
            (text or "").lower(),
            re.DOTALL,
        )
        if khan_total:
            d = normalize_amount(khan_total.group(1))
            c = normalize_amount(khan_total.group(2))
            if d is not None and abs(d) > 100:
                expense = abs(d)
            if c is not None and abs(c) > 100:
                income = abs(c)

    if "opening_balance" in summary:
        opening = summary["opening_balance"]
    else:
        opening, _ = derive_balances(txs, income, expense)
    if "closing_balance" in summary:
        closing = summary["closing_balance"]
    else:
        _, closing = derive_balances(txs, income, expense)

    # Khan Bank-д extract_summary_amounts нь column header
    # "Эхний үлдэгдэл / Эцсийн үлдэгдэл"-ийг "summary line" гэж буруу таагаад
    # эхний мөрийн opening_balance-ыг эцсийн үлдэгдэл болгож тавьдаг алдаа байсан.
    # Khan-д үргэлж транзакцуудаас бодсон утгыг ашиглана.
    if bank_hint == "Khan Bank" and txs:
        first = txs[0]
        # ParsedTransaction.balance = closing balance of that row
        signed = first.amount if first.type == "income" else -first.amount
        opening = first.balance - signed
        closing = txs[-1].balance

    # Хэрэв гүйлгээний нийлбэр нь summary-аас зөрж байгаа бол ангилал-ыг
    # дахин жинлэнэ — total_expense дотор % нь зөв байгаа эсэхийг хангана.
    if breakdown and expense > 0:
        sum_in_breakdown = sum(c.amount for c in breakdown) or 1.0
        scale = expense / sum_in_breakdown if sum_in_breakdown > 0 else 1.0
        for c in breakdown:
            c.amount = c.amount * scale
            c.percentage = (c.amount / expense) * 100

    # Counterparty breakdown + pattern insights
    counterparty_breakdown = aggregate_counterparties(txs)
    patterns = detect_patterns(txs, counterparty_breakdown)

    log.info(
        "parsed bank=%s tx=%d income=%.0f expense=%.0f opening=%.0f closing=%.0f counterparties=%d patterns=%d summary=%s",
        bank_name,
        len(txs),
        income,
        expense,
        opening,
        closing,
        len(counterparty_breakdown),
        len(patterns),
        list(summary.keys()),
    )

    return ParsedStatement(
        bank_name=bank_name,
        opening_balance=opening,
        closing_balance=closing,
        total_income=income,
        total_expenses=expense,
        period_start=period_start,
        period_end=period_end,
        transactions=txs,
        category_breakdown=breakdown,
        counterparty_breakdown=counterparty_breakdown,
        patterns=patterns,
    )
