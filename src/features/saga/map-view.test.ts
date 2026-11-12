import { describe, expect, it } from 'vitest';
import { cameraView, type MapCamera } from './map-view';

/** A camera whose ratio changes only when its move ends, as while the animation of a press still runs. */
function slowCamera(ratio = 1) {
  const aims: number[] = [];
  let resets = 0;
  const pending: Array<() => void> = [];
  const camera: MapCamera = {
    getState: () => ({ ratio }),
    getBoundedRatio: (wanted) => Math.min(8, Math.max(0.05, wanted)),
    animate: (state) => new Promise<void>((resolve) => {
      aims.push(state.ratio);
      pending.push(() => { ratio = state.ratio; resolve(); });
    }),
    animatedReset: () => { resets += 1; return Promise.resolve(); },
  };
  return { aims, camera, end: () => pending.splice(0).forEach((finish) => finish()), resets: () => resets };
}

// specs/2026-11-11-ux8-map.md MP1.1: every press of «+» and «−» is a step, also a press made while the camera still moves.
describe('the view of the saga map by its buttons', () => {
  it('adds up the presses made while the camera still moves', () => {
    const { aims, camera } = slowCamera(1);
    const view = cameraView(camera, { duration: 250 });
    view.zoomIn();
    view.zoomIn();
    view.zoomIn();
    expect(aims.map((aim) => Number(aim.toFixed(4)))).toEqual([Number((1 / 1.5).toFixed(4)), Number((1 / 1.5 ** 2).toFixed(4)), Number((1 / 1.5 ** 3).toFixed(4))]);
  });

  it('steps from where the camera stands once its move has ended, and back out', async () => {
    const { aims, camera, end } = slowCamera(1);
    const view = cameraView(camera, { duration: 250 });
    view.zoomIn();
    end();
    await Promise.resolve();
    view.zoomOut();
    expect(Number(aims[1]!.toFixed(4))).toBe(1);
  });

  it('keeps the steps within what the camera allows and forgets them on «Показать всё»', () => {
    const { aims, camera, resets } = slowCamera(0.06);
    const view = cameraView(camera, { duration: 250 });
    view.zoomIn();
    view.zoomIn();
    expect(aims).toEqual([0.05, 0.05]);
    view.showAll();
    expect(resets()).toBe(1);
    view.zoomOut();
    // After the reset the step is counted from the camera again, not from the aim before it.
    expect(Number(aims[2]!.toFixed(4))).toBe(Number((0.06 * 1.5).toFixed(4)));
  });
});
