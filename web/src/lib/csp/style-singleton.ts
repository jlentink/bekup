/**
 * CSP-safe drop-in for `react-style-singleton` (aliased in vite.config.ts).
 *
 * The original appends <style> elements at runtime, which `style-src 'self'`
 * blocks. react-remove-scroll only uses it to lock body scrolling while a modal
 * layer is open; the same rules are shipped statically in
 * src/styles/csp-compat.css, keyed on the `data-scroll-locked` attribute that
 * react-remove-scroll-bar still sets through the DOM API.
 */
import type { ComponentType } from "react";

interface StyleProps {
  styles: string;
  dynamic?: boolean;
}

export const stylesheetSingleton = (): { add: (style: string) => void; remove: () => void } => ({
  add: () => undefined,
  remove: () => undefined,
});

export const styleHookSingleton = (): ((styles: string, isDynamic?: boolean) => void) => () =>
  undefined;

export const styleSingleton = (): ComponentType<StyleProps> => {
  const NoStyle: ComponentType<StyleProps> = () => null;
  return NoStyle;
};
