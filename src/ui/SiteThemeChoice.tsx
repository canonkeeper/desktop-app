import { siteThemeChoices, useSiteTheme } from '../shared/theme/site-theme';
import { SegmentedControl } from './navigation/SegmentedControl';

/**
 * «Тема: Светлая · Тёмная · Как в системе» (specs/2026-11-12-ux11-dark-core.md TH1.2; Pattern/Shell/Account Menu,
 * Pattern/Account/Account Page): three buttons of one choice, the same in the menu of the account and on «Аккаунт». The
 * choice is painted at once and kept in this browser.
 */
export function SiteThemeChoice({ className, fullWidth = false }: { className?: string; fullWidth?: boolean }) {
  const site = useSiteTheme();
  return <SegmentedControl className={className} fullWidth={fullWidth} label="Тема" onValueChange={site.choose} options={siteThemeChoices} value={site.choice} />;
}
