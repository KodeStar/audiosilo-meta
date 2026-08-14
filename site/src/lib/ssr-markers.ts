// The body half of the injection contract metaserve renders the entity pages
// through (the head half is the `ssr:head` pair in layouts/Base.astro).
//
// The marker has to survive the Astro build as a literal HTML comment in the
// built shell, and two obvious spellings do NOT:
//
//   <!--ssr:entity-->                 stripped - Astro's compiler drops HTML
//                                     comments out of a component's SLOT
//                                     content (comments inside a component's
//                                     own template, like Base.astro's, stay)
//   <Fragment set:html="<!--...-->" /> escaped - a quoted set:html attribute is
//                                     treated as text, so the built page gets
//                                     &lt;!--ssr:entity--&gt;
//
// What works is set:html given an EXPRESSION, which injects the string raw:
//
//   <Fragment set:html={ENTITY_MARKER} />
//
// Hence this constant. It is deliberately NOT imported by the guard test
// (src/lib/dist-markers.test.ts spells the marker out): a shared constant would
// let a rename move the page and the guard together and quietly change the wire
// contract the Go side splits on.
export const ENTITY_MARKER = '<!--ssr:entity-->'
