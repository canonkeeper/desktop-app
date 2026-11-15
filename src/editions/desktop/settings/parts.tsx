// SPDX-License-Identifier: AGPL-3.0-only
import { Bot, CircleUserRound, HardDrive, Info, RefreshCw, ScrollText, SunMoon, type LucideIcon } from 'lucide-react';
import type { ComponentType } from 'react';
import { AccountPart } from '../account/AccountPart';
import { AboutPart } from './AboutPart';
import { DataPart } from './DataPart';
import { HelperPart } from './helper/HelperPart';
import { LogPart } from './log/LogPart';
import { LookPart } from './LookPart';
import { UpdatesPart } from '../updates/UpdatesPart';
import { accountPartKey, helperPartKey, logPartKey, updatesPartKey, type SettingsPartProps } from './part-props';

export interface SettingsPart {
  /** The part in the address: `/settings?part=<key>`. */
  key: string;
  label: string;
  Icon: LucideIcon;
  Part: ComponentType<SettingsPartProps>;
}

/**
 * The parts of «Настройки» of the app in the order of the board (specs/2026-11-13-desk-settings.md ST1.2, ST1.3; board 44,
 * `Pattern/Desktop/Settings`): «Аккаунт», «Помощник», «Журнал», «Данные», «Вид», «Обновления», «О программе». A part
 * stands here once it works: a slice that brings «Аккаунт», «Помощник», «Журнал» or «Обновления» adds its row and its
 * screen, and the page stays as it is.
 */
export const settingsParts: readonly SettingsPart[] = [
  // The link with an account (specs/2026-11-15-desk-account-link-web.md AW2.1, board 44·6, 44·7).
  { key: accountPartKey, label: 'Аккаунт', Icon: CircleUserRound, Part: AccountPart },
  // «Свой ИИ» (specs/2026-11-13-desk-helper-web.md HW1.1).
  { key: helperPartKey, label: 'Помощник', Icon: Bot, Part: HelperPart },
  // The journal of what went out (specs/2026-11-14-desk-journal.md JR3.1, board 44·10).
  { key: logPartKey, label: 'Журнал', Icon: ScrollText, Part: LogPart },
  { key: 'data', label: 'Данные', Icon: HardDrive, Part: DataPart },
  { key: 'look', label: 'Вид', Icon: SunMoon, Part: LookPart },
  // specs/2026-11-15-desk-updates-web.md UW2.1 (board 44·14).
  { key: updatesPartKey, label: 'Обновления', Icon: RefreshCw, Part: UpdatesPart },
  { key: 'about', label: 'О программе', Icon: Info, Part: AboutPart },
];

/** The part a page without a part in its address, or with one it does not know, opens. */
export const firstSettingsPart = 'data';

/** The part the address names; «Данные» when it names none or one there is not. */
export function settingsPartOf(key: string | null): SettingsPart {
  return settingsParts.find((part) => part.key === key) ?? settingsParts.find((part) => part.key === firstSettingsPart)!;
}
