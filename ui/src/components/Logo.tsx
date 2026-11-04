import { useEffect, useId, useRef } from "react";

// The tapir of tapyr.eu, with its eyes and the sniffing braces. It blinks
// twice when it appears, then when hovered; the dots pulse on hover.
const reduceMotion = () => typeof window !== "undefined" && window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;

const body =
  "M28 42 C36 28 66 24 94 27 C104 28 111 31 116 35 C121 33 128 33 134 37 C140 41 144 46 148 52 C152 57 155 62 154 68 C153 72 148 73 147 69 C146 65 142 63 138 64 C135 67 131 69 127 68 C123 70 119 73 117 77 C115 82 110 84 104 84 L46 84 C32 84 22 74 22 62 C22 52 24 46 28 42 Z";

const delay = 600; // first blink, in milliseconds

export function Tapir({ title }: { title?: string }) {
  const eyes = useRef<SVGGElement>(null);
  const clip = useId();
  const blink = () => eyes.current?.querySelectorAll("animate").forEach((a) => (a as SVGAnimationElement).beginElement?.());
  useEffect(() => {
    if (reduceMotion()) return;
    const a = setTimeout(blink, delay);
    const b = setTimeout(blink, delay + 2000);
    return () => {
      clearTimeout(a);
      clearTimeout(b);
    };
  }, []);
  return (
    <svg
      viewBox="0 0 186 100"
      className="sniffable"
      role={title ? "img" : undefined}
      aria-hidden={title ? undefined : true}
      aria-label={title}
      onMouseEnter={() => !reduceMotion() && blink()}
    >
      <defs>
        <clipPath id={clip}>
          <path d={body} />
        </clipPath>
      </defs>
      <g stroke="var(--t-line)" strokeWidth="3" strokeLinejoin="round" strokeLinecap="round">
        <rect x="106" y="72" width="11" height="23" rx="5" fill="var(--t-body-2)" />
        <rect x="44" y="72" width="11" height="23" rx="5" fill="var(--t-body-2)" />
        <ellipse cx="120" cy="31" rx="6" ry="9.5" transform="rotate(14 120 31)" fill="var(--t-body)" />
        <path d={body} fill="var(--t-body)" />
        <path clipPath={`url(#${CSS.escape(clip)})`} d="M70 16 C62 40 60 64 64 92 L0 92 L0 16 Z" fill="var(--t-saddle)" stroke="none" />
        <path d={body} fill="none" />
        <rect x="96" y="74" width="12" height="24" rx="5.5" fill="var(--t-body)" />
        <rect x="33" y="74" width="12" height="24" rx="5.5" fill="var(--t-body)" />
      </g>
      <ellipse cx="120.2" cy="31.6" rx="3.6" ry="6.6" transform="rotate(14 120.2 31.6)" fill="none" stroke="var(--t-saddle)" strokeWidth="2" />
      <ellipse cx="151" cy="69.5" rx="1.1" ry="1.7" fill="var(--t-eye)" opacity=".55" />
      <g ref={eyes}>
        <ellipse cx="133" cy="46" rx="3.6" ry="3.6" fill="var(--t-eye-white)">
          <animate attributeName="ry" values="3.6;0.4;3.6" dur="0.28s" begin="indefinite" />
        </ellipse>
        <ellipse cx="134" cy="46" rx="2.2" ry="2.2" fill="var(--t-eye)">
          <animate attributeName="ry" values="2.2;0.3;2.2" dur="0.28s" begin="indefinite" />
        </ellipse>
      </g>
      <g fill="none" stroke="var(--t-brace)" strokeWidth="2.8" strokeLinecap="round" strokeLinejoin="round">
        <path d="M156 9 C152 9 152 11 152 14 L152 18 C152 20 151 21 149 21 C151 21 152 22 152 24 L152 28 C152 31 152 33 156 33" />
        <path d="M176 9 C180 9 180 11 180 14 L180 18 C180 20 181 21 183 21 C181 21 180 22 180 24 L180 28 C180 31 180 33 176 33" />
      </g>
      <g className="sniff" fill="var(--t-dot)">
        <circle cx="160.5" cy="21" r="2.4" />
        <circle cx="166" cy="21" r="2.4" />
        <circle cx="171.5" cy="21" r="2.4" />
      </g>
    </svg>
  );
}

export function Brand() {
  return (
    <a className="brand" href="#/" aria-label="tapyr, overview">
      <Tapir />
      <span>
        tap<span className="y-accent">y</span>r
      </span>
    </a>
  );
}
