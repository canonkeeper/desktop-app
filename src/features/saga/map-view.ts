/**
 * The view of the saga map (specs/2026-11-11-ux8-map.md MP1.1): «+», «−» and «Показать всё» move the camera of the canvas,
 * and so do the keys «+», «−», «0» while the focus is on the map.
 */
export interface MapView {
  zoomIn: () => void;
  zoomOut: () => void;
  /** The view and the scale the map opened with. */
  showAll: () => void;
}

export type MapViewMove = keyof MapView;

/** What the view needs of the camera of the canvas. */
export interface MapCamera {
  getState: () => { ratio: number };
  getBoundedRatio: (ratio: number) => number;
  animate: (state: { ratio: number }, options: { duration: number }) => Promise<void>;
  animatedReset: (options: { duration: number }) => Promise<void>;
}

/** One press of «+» or «−» changes the scale by this much. */
const zoomStep = 1.5;

/**
 * The moves of the camera by the buttons and the keys. Every press is a step: a press made while the camera still moves
 * is counted from where the camera is going, not from where it is — or three quick presses would move it by one.
 */
export function cameraView(camera: MapCamera, move: { duration: number }): MapView {
  let aim: number | null = null;
  let moves = 0;
  const step = (factor: number) => {
    const ratio = camera.getBoundedRatio((aim ?? camera.getState().ratio) * factor);
    aim = ratio;
    moves += 1;
    const mine = moves;
    void camera.animate({ ratio }, move).then(() => {
      if (mine === moves) aim = null;
    });
  };
  return {
    zoomIn: () => step(1 / zoomStep),
    zoomOut: () => step(zoomStep),
    showAll: () => {
      aim = null;
      moves += 1;
      void camera.animatedReset(move);
    },
  };
}

const keys: Readonly<Record<string, MapViewMove>> = { '+': 'zoomIn', '=': 'zoomIn', '-': 'zoomOut', '−': 'zoomOut', _: 'zoomOut', '0': 'showAll' };

/** The move a key asks for on the map; none for a key with a modifier or typed into a field (the search of the map). */
export function viewKey(event: Pick<KeyboardEvent, 'altKey' | 'ctrlKey' | 'key' | 'metaKey' | 'target'>): MapViewMove | null {
  if (event.altKey || event.ctrlKey || event.metaKey) return null;
  const target = event.target as HTMLElement | null;
  if (target && (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))) return null;
  return keys[event.key] ?? null;
}
