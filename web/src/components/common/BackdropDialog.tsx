import React, { forwardRef, useRef } from 'react';

type Props = React.DialogHTMLAttributes<HTMLDialogElement> & { onClose: () => void };

/**
 * <dialog> that closes when its backdrop is clicked, but only if the press also
 * started on the backdrop. Dragging a text selection out of the dialog and
 * releasing over the backdrop must not close it.
 */
export const BackdropDialog = forwardRef<HTMLDialogElement, Props>(function BackdropDialog(
  { onClose, onCancel, children, ...rest },
  ref,
) {
  const pressedOnBackdrop = useRef(false);
  return (
    <dialog
      {...rest}
      ref={ref}
      onCancel={onCancel ?? onClose}
      onPointerDown={(event) => { pressedOnBackdrop.current = event.target === event.currentTarget; }}
      onClick={(event) => {
        const started = pressedOnBackdrop.current;
        pressedOnBackdrop.current = false;
        if (event.target === event.currentTarget && started) onClose();
      }}
    >
      {children}
    </dialog>
  );
});
