/*
  The chart panel's range switch as DOM (decision 9 of
  project/2026-09-27-chart-panel-revision.md): the burn-up already on
  the page slides from the old island's window and y-domain to the new
  one's, redrawn each frame by chart-geometry.mjs from the server's
  samples; then the caller swaps in the server's fragment, so the
  resting drawing is always the server's.

  What does not morph: the gridlines stay where they are (a domain
  mid-flight is a value no axis settles on, and gridlines re-picked
  every frame would flicker between densities), and the histogram, the
  end labels and the axis and gridline labels fade out and back in —
  the histogram's bars change count between ranges (12 on 1H, 28 on
  7D), so there is no bar-to-bar path to interpolate. The stylesheet
  does the fading off data-chart-anim, which this sets to "slide" for
  the move and "land" for the fade back in.

  Timing is the design system's, read from the motion tokens: the slide
  takes --motion-duration-slow on --motion-ease-out, the fade back in
  --motion-duration-base.
*/

import { burnupPaths, markersAt } from "./chart-geometry.mjs";
import { frameIsland, mergedTrace, parseDuration, parseEase, runSlide } from "./chart-motion.mjs";

export const ANIM_ATTR = "data-chart-anim";
export const AnimPhase = Object.freeze({ Slide: "slide", Land: "land" });

const SLIDE_FALLBACK_MS = 320;
const SETTLE_FALLBACK_MS = 200;

// motionTokens reads the slide's timing off the stylesheet's tokens.
function motionTokens() {
  const cs = window.getComputedStyle(document.documentElement);
  return {
    duration: parseDuration(cs.getPropertyValue("--motion-duration-slow"), SLIDE_FALLBACK_MS),
    settle: parseDuration(cs.getPropertyValue("--motion-duration-base"), SETTLE_FALLBACK_MS),
    ease: parseEase(cs.getPropertyValue("--motion-ease-out")),
  };
}

const PATHS = {
  scope: ".c-burnup__scope",
  done: ".c-burnup__done",
  gap: ".c-burnup__gap",
  blocked: ".c-burnup__blocked",
  canceled: ".c-burnup__canceled",
};

// slide moves host's burn-up from island from to island to, then calls
// land() — which swaps in the new fragment — once, from the last frame
// or from the watchdog. It returns a function that stops the slide and
// lands at once, for a newer swap that cannot wait.
export function slide(host, from, to, land) {
  const { duration, settle, ease } = motionTokens();
  const merged = mergedTrace(from, to);
  const endIndex = merged.indexOf(to.trace[to.trace.length - 1]);
  const paths = Object.entries(PATHS)
    .map(([key, sel]) => [key, host.querySelector(sel)])
    .filter(([, el]) => el);
  const scopeDot = host.querySelector(".c-burnup__dot--scope");
  const doneDot = host.querySelector(".c-burnup__dot--done");

  let landed = false;
  const settleThenLand = () => {
    if (landed) return;
    landed = true;
    land();
    host.setAttribute(ANIM_ATTR, AnimPhase.Land);
    window.setTimeout(() => {
      if (host.getAttribute(ANIM_ATTR) === AnimPhase.Land) host.removeAttribute(ANIM_ATTR);
    }, settle);
  };

  host.setAttribute(ANIM_ATTR, AnimPhase.Slide);
  const cancel = runSlide({
    duration,
    ease,
    draw: (p) => {
      const frame = frameIsland(from, to, p, merged);
      const d = burnupPaths(frame);
      for (const [key, el] of paths) el.setAttribute("d", d[key]);
      const m = markersAt(frame, endIndex);
      if (scopeDot) {
        scopeDot.setAttribute("cx", m.scopeDot.x);
        scopeDot.setAttribute("cy", m.scopeDot.y);
      }
      if (doneDot) {
        doneDot.setAttribute("cx", m.doneDot.x);
        doneDot.setAttribute("cy", m.doneDot.y);
      }
    },
    land: settleThenLand,
    raf: (fn) => window.requestAnimationFrame(fn),
    caf: (id) => window.cancelAnimationFrame(id),
    setTimer: (fn, ms) => window.setTimeout(fn, ms),
    clearTimer: (id) => window.clearTimeout(id),
    now: () => window.performance.now(),
  });
  return () => {
    cancel();
    settleThenLand();
  };
}
