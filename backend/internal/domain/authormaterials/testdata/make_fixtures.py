#!/usr/bin/env python3
"""Writes the synthetic files of the author's materials (specs/2026-11-04-saga-map.md, UC9) with the libraries other
programs write such files with: openpyxl for the workbook, python-docx for the document. Run from this folder; a third
copy of the workbook goes through LibreOffice: soffice --headless --convert-to xlsx --outdir libre materials.xlsx"""
from docx import Document
from openpyxl import Workbook
from openpyxl.comments import Comment
from openpyxl.styles import Font, PatternFill

ORANGE = PatternFill("solid", fgColor="FFC000")
PURPLE = PatternFill("solid", fgColor="7030A0")

book = Workbook()
skills = book.active
skills.title = "Навыки"
skills.append(["Навык", "Редкость"])
rows = [
    ("Миг назад", "уникальный", ORANGE, "Возвращает бой на миг назад.\nЗатраты эфира: 10"),
    ("одушевление", "эпический", ORANGE, None),
    ("пустота в ладонях", "наследие", PURPLE, "Описание: Гасит чужое заклинание.\nРанг: Наследие"),
    ("омут снов", "эпический", ORANGE,
     "Улучшить навык: [ужас] → [омут снов]\nРанг: Уникальный (вне уровней)\nОписание: Сны становятся явью.\nЗатраты эфира: 40\nПримечание: только ночью"),
]
for name, rarity, fill, note in rows:
    skills.append([name, rarity])
    row = skills.max_row
    skills.cell(row, 2).fill = fill
    if note:
        skills.cell(row, 1).comment = Comment(note, "Автор")

places = book.create_sheet("Места")
places.append(["Название", "Описание", "Этаж"])
places.append(["Студена", "Столица севера, там живёт Мирон.", 3])
places.append(["Башня Эха", "Построена гномами.", 12])
places.cell(3, 3).font = Font(color="C00000")

people = book.create_sheet("Персонажи")
people.append(["Имя", "Заметка"])
people.append(["Мирон", "Главный герой, уже есть в книге."])
people.append(["Молли", "Сестра героя."])

hidden = book.create_sheet("Черновики")
hidden.append(["Секрет", "не для разбора"])
hidden.sheet_state = "hidden"
book.save("materials.xlsx")

document = Document()
document.add_heading("Места", 1)
document.add_heading("Студена", 2)
city = document.add_paragraph("Столица севера.")
document.add_comment(city.runs, text="Население: 40 тысяч", author="Автор")
document.add_paragraph("Там живёт Мирон.")
document.add_heading("Навыки", 1)
table = document.add_table(rows=3, cols=2)
table.cell(0, 0).text, table.cell(0, 1).text = "Навык", "Ранг"
table.cell(1, 0).text, table.cell(1, 1).text = "одушевление", "эпический"
table.cell(2, 0).text, table.cell(2, 1).text = "омут снов", "уникальный"
document.add_comment(table.cell(2, 0).paragraphs[0].runs, text="Сны становятся явью.", author="Автор")
document.save("materials.docx")
