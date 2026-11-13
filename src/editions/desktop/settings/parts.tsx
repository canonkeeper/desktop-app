// SPDX-License-Identifier: AGPL-3.0-only
import { HardDrive, Info, SunMoon, type LucideIcon } from 'lucide-react';
import type { ComponentType } from 'react';
import { AboutPart } from './AboutPart';
import { DataPart } from './DataPart';
import { LookPart } from './LookPart';
import type { SettingsPartProps } from './part-props';

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
  { key: 'data', label: 'Данные', Icon: HardDrive, Part: DataPart },
  { key: 'look', label: 'Вид', Icon: SunMoon, Part: LookPart },
  { key: 'about', label: 'О программе', Icon: Info, Part: AboutPart },
];

/** The part a page without a part in its address, or with one it does not know, opens. */
export const firstSettingsPart = 'data';

/** The part the address names; «Данные» when it names none or one there is not. */
export function settingsPartOf(key: string | null): SettingsPart {
  return settingsParts.find((part) => part.key === key) ?? settingsParts.find((part) => part.key === firstSettingsPart)!;
}
